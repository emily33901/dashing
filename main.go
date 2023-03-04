package main

import (
	"bufio"
	"fmt"
	"io"
	"os"

	log "github.com/sirupsen/logrus"
)

func main() {
	url := os.Args[1]
	videoId := url[len(url)-11:]

	logFile, err := os.Create(fmt.Sprintf("output/%s.log", videoId))
	if err != nil {
		panic(err)
	}
	log.SetOutput(io.MultiWriter(os.Stderr, logFile))

	log.Println(url)

	ytdlResult, err := ytdl(os.Args[1])

	if err != nil {
		panic(err)
	}

	var bestAudioFormat *YtdlResultFormat = nil
	var bestVideoFormat *YtdlResultFormat = nil

	for i, format := range ytdlResult.Formats {
		typ := format.typ()

		if typ == YtdlFormatTypeAudio {
			log.Println("format audio", format.ID, *format.AudioSampleRate, "hz", "bitrate", *format.AudioBitRate)
			if bestAudioFormat == nil || *format.AudioBitRate > *bestAudioFormat.AudioBitRate {
				bestAudioFormat = &ytdlResult.Formats[i]
			}
		} else if typ == YtdlFormatTypeVideo {
			log.Println("format video", format.ID, *format.Width, "x", *format.Height, "@", *format.FPS, "bitrate", *format.VideoBitRate)
			if bestVideoFormat == nil || *format.VideoBitRate > *bestVideoFormat.VideoBitRate {
				bestVideoFormat = &ytdlResult.Formats[i]
			}
		}
	}

	log.Println("chose format audio", bestAudioFormat.ID, *bestAudioFormat.AudioSampleRate, "hz", "bitrate", *bestAudioFormat.AudioBitRate)
	log.Println("chose format video", bestVideoFormat.ID, *bestVideoFormat.Width, "x", *bestVideoFormat.Height, "@", *bestVideoFormat.FPS, "bitrate", *bestVideoFormat.VideoBitRate)

	log.Println("formats expire at", bestVideoFormat.ExpiryDeadline())

	outputFile, err := os.Create(fmt.Sprintf("output/%s.mp4", videoId))
	if err != nil {
		panic(err)
	}
	defer outputFile.Close()

	c := DashClient{
		id:          ytdlResult.DisplayID,
		output:      bufio.NewWriter(outputFile),
		audioFormat: bestAudioFormat.AsDashFormat(),
		videoFormat: bestVideoFormat.AsDashFormat(),
	}

	c.Start()
	err = c.Wait()

	if err != nil {
		log.Println("FFMpeg error: ", c.ffmpegError.String())
		panic(err)
	}

	log.Println("FFMpeg success:", c.ffmpegError.String())
}
