package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ServerInfo struct {
	HeadSequence int
	HeadTime     time.Duration
	WallTime     time.Time
}

type YtdlResultFormat struct {
	ManifestUrl     *string  `json:"manifest_url"`
	FragmentBaseUrl *string  `json:"fragment_base_url"`
	Url             *string  `json:"url"`
	TotalBitRate    float32  `json:"tbr"`
	VideoBitRate    *float32 `json:"vbr"`
	AudioBitRate    *float32 `json:"abr"`
	AudioSampleRate *float32 `json:"asr"`
	Width           *int     `json:"width"`
	Height          *int     `json:"height"`
	FPS             *float32 `json:"fps"`
	ID              string   `json:"format_id"`
}

const (
	YtdlFormatTypeVideo      = 0
	YtdlFormatTypeAudio      = iota
	YtdlFormatTypeStoryBoard = iota
	YtdlFormatTypeBoth       = iota
)

func (format *YtdlResultFormat) typ() int {
	hasAudio := format.AudioBitRate != nil && *format.AudioBitRate != 0
	hasVideo := format.VideoBitRate != nil && *format.VideoBitRate != 0

	if hasAudio && !hasVideo {
		return YtdlFormatTypeAudio
	} else if !hasAudio && !hasVideo {
		return YtdlFormatTypeStoryBoard
	} else if hasAudio && hasVideo {
		return YtdlFormatTypeBoth
	}
	return YtdlFormatTypeVideo
}

var expireRegex *regexp.Regexp

func (format *YtdlResultFormat) BaseURL() *string {
	if format.FragmentBaseUrl != nil {
		return format.FragmentBaseUrl
	}
	return format.Url
}

func (format *YtdlResultFormat) ExpiryDeadline() time.Time {
	stringUrl := format.BaseURL()

	// Try and parse the URL and get from parameter list
	url, err := url.Parse(*stringUrl)
	if err != nil {
		panic(err)
	}
	if expire := url.Query().Get("expire"); expire != "" {
		expire, _ := strconv.ParseInt(expire, 10, 64)
		return time.Unix(int64(expire), 0)
	}

	// No query, try and regex it out
	if expireRegex == nil {
		newExpireRegex, err := regexp.Compile(`(?:/|\?)expire/(\d+)/`)
		if err != nil {
			panic(err)
		}

		expireRegex = newExpireRegex
	}

	matches := expireRegex.FindSubmatch([]byte(*stringUrl))
	expire, _ := strconv.ParseInt(string(matches[1]), 10, 64)
	return time.Unix(int64(expire), 0)
}

func (format *YtdlResultFormat) AsDashFormat() DashFormat {
	return &YoutubeDashFormat{
		originalUrl: *format.Url,
		format:      *format,
		lock:        sync.RWMutex{},
	}
}

type YtdlResult struct {
	Formats    []YtdlResultFormat `json:"formats"`
	LiveStatus string             `json:"live_status"`
	DisplayID  string             `json:"display_id"`
}

var (
	ErrNotStarted                = errors.New("live event has not started yet")
	ErrAlreadyFinished           = errors.New("live even has already finished")
	ErrVideoNotExist             = errors.New("video does not exist")
	ErrUnableToExtractUploaderId = errors.New("unable to extract uploader id")
)

func ytdlParseError(e string) error {
	if strings.Contains(e, "ERROR") {
		if strings.Contains(e, "This live event will begin in") ||
			strings.Contains(e, "Premieres in") {
			return ErrNotStarted
		}

		if strings.Contains(e, "Unable to extract uploader id") {
			return ErrUnableToExtractUploaderId
		}

		if strings.Contains(e, "Video unavailable") {
			return ErrVideoNotExist
		}
	}
	return fmt.Errorf("unknown error '%s'", e)
}

// getManifestUrl gets a manifest URL from a youtube URL
func ytdl(url string) (*YtdlResult, error) {
	stderr := &strings.Builder{}
	stdout := &strings.Builder{}

	cmd := exec.Command("yt-dlp", "--no-warnings", "-j", "--live-from-start", url)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	if err != nil {
		return nil, ytdlParseError(stderr.String())
	}

	var result YtdlResult
	decoder := json.NewDecoder(strings.NewReader(stdout.String()))
	err = decoder.Decode(&result)
	if err != nil {
		log.Println("ytdlp did not produce valid json")

		return nil, ErrNotStarted
	}

	return &result, nil
}

const (
	DashFormatTypeUnknown = 0
	DashFormatTypeAudio   = iota
	DashFormatTypeVideo   = iota
	DashFormatTypeBoth    = iota
)

type DashFormat interface {
	// SegmentURL for sequence returns the URL for the segment referred to by the sequence
	SegmentURLForSequence(seq int) (string, error)
	SegmentDuration() (time.Duration, error)
	ServerInfo(header http.Header) (ServerInfo, error)
}

type YoutubeDashFormat struct {
	originalUrl string
	format      YtdlResultFormat
	lock        sync.RWMutex
}

var (
	errUrlExpiring     = errors.New("dash format is close to expiring")
	errUnableToRefresh = errors.New("unable to refresh dash format")
	errFormatMissing   = errors.New("dash format was missing from new result")
)

func (f *YoutubeDashFormat) segmentUrlForSequence(seq int) (url string, err error) {
	// Check whether this format is near to expiring
	f.lock.RLock()
	defer f.lock.RUnlock()
	if time.Until(f.format.ExpiryDeadline()) > time.Minute*20 {
		url = fmt.Sprintf("%s?sq=%d&rn=0&rbuf=0", *f.format.FragmentBaseUrl, seq)
		return
	}
	err = errUrlExpiring
	return
}

func (f *YoutubeDashFormat) Update() error {
	f.lock.Lock()
	defer f.lock.Unlock()
	result, err := ytdl(f.originalUrl)
	if err != nil {
		return err
	}
	// Find our format id in the formats
	for _, newFormat := range result.Formats {
		if newFormat.ID == f.format.ID {
			f.format = newFormat
			return nil
		}
	}
	return errFormatMissing
}

func (f *YoutubeDashFormat) SegmentURLForSequence(seq int) (url string, err error) {
	url, err = f.segmentUrlForSequence(seq)
	if err == nil {
		return
	}
	// Format is near to expiring, try and update
	err = f.Update()
	if err != nil {
		return
	}
	// Now that we have updated, try again
	url, err = f.segmentUrlForSequence(seq)
	return
}

func (f *YoutubeDashFormat) SegmentDuration() (time.Duration, error) {
	url, err := f.SegmentURLForSequence(0)
	if err != nil {
		return 0, err
	}
	response, err := http.Head(url)
	if err != nil {
		return 0, err
	}

	info, err := f.ServerInfo(response.Header)
	if err != nil {
		return 0, err
	}

	segmentDuration := int(float32(info.HeadTime) / float32(info.HeadSequence))
	return time.Duration(segmentDuration), nil
}

func (f *YoutubeDashFormat) ServerInfo(header http.Header) (info ServerInfo, err error) {
	info.HeadSequence, err = strconv.Atoi(header.Get("X-Head-SeqNum"))
	if err != nil {
		return
	}
	timeMs, err := strconv.Atoi(header.Get("X-Head-Time-Millis"))
	if err != nil {
		return
	}
	info.HeadTime = time.Duration(timeMs) * time.Millisecond

	walltimeMs, err := strconv.ParseInt(header.Get("X-Walltime-Ms"), 10, 64)
	if err != nil {
		return
	}
	info.WallTime = time.UnixMilli(walltimeMs)

	return
}

var _ DashFormat = (*YoutubeDashFormat)(nil)
