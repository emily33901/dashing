package main

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
)

type OnPipe func(pipe io.ReadWriteCloser)

type Ffmpeg struct {
	id     string
	cmd    *exec.Cmd
	errLog *strings.Builder
}

func NewFfmpeg(id string) *Ffmpeg {
	return &Ffmpeg{
		id: id,
	}
}

func (f *Ffmpeg) Start(onAudioPipe, onVideoPipe, onOutputPipe OnPipe) error {
	cmd, stderr, err := f.start(f.id, onAudioPipe, onVideoPipe, onOutputPipe)

	if err != nil {
		return err
	}

	f.cmd = cmd
	f.errLog = stderr

	return nil
}

func (f *Ffmpeg) Wait() error {
	return f.cmd.Wait()
}

func (f *Ffmpeg) Err() *strings.Builder {
	return f.errLog
}

func (f *Ffmpeg) makeCmd(aname, vname, oname string) *exec.Cmd {
	return exec.Command(
		"ffmpeg",
		"-thread_queue_size", fmt.Sprintf("%d", 1024),
		"-i", vname,
		"-thread_queue_size", fmt.Sprintf("%d", 1024),
		"-i", aname,
		"-map", "0",
		"-map", "1",
		"-c", "copy",
		"-f", "mp4",
		"-movflags", "frag_keyframe",
		"-hide_banner",
		"-y",
		oname)
}
