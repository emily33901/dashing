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

	if ytdlResult.LiveStatus != YtdlLiveStatusLive {
		panic("video is not live")
	}

	bestAudioFormat, bestVideoFormat := ytdlResult.BestFormats()

	if bestAudioFormat == nil || bestVideoFormat == nil {
		panic("failed to find a video or audio format")
	}

	log.Println("chose format audio", bestAudioFormat.ID, *bestAudioFormat.AudioSampleRate, "hz", "bitrate", *bestAudioFormat.AudioBitRate)
	log.Println("chose format video", bestVideoFormat.ID, *bestVideoFormat.Width, "x", *bestVideoFormat.Height, "@", *bestVideoFormat.FPS, "bitrate", *bestVideoFormat.VideoBitRate)

	log.Println("formats expire at", bestVideoFormat.ExpiryDeadline())

	outputFile, err := os.Create(fmt.Sprintf("output/%s.mp4", videoId))
	if err != nil {
		panic(err)
	}
	defer outputFile.Close()

	bestDashAudioFormat, err := bestAudioFormat.AsDashFormat()
	if err != nil {
		panic(err)
	}

	bestDashVideoFormat, err := bestVideoFormat.AsDashFormat()
	if err != nil {
		panic(err)
	}

	c := NewDashClient(ytdlResult.DisplayID, bestDashAudioFormat, bestDashVideoFormat, bufio.NewWriter(outputFile))

	c.Start()
	err = c.Wait()

	if err != nil {
		log.Println("ffmpeg error: ", c.FfmpegError())
		panic(err)
	}

	log.Println("ffmpeg success:", c.FfmpegError())
}
