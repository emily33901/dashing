package main

import (
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
