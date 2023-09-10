package main

import (
	"io"
	"os/exec"
	"strings"
)

type Ffmpeg struct {
	id     string
	cmd    *exec.Cmd
	errLog *strings.Builder
	apipe  io.ReadWriteCloser
	vpipe  io.ReadWriteCloser
	opipe  io.ReadWriteCloser
}

func (f *Ffmpeg) Start(id string) error {
	f.id = id
	cmd, apipe, vpipe, opipe, stderr, err := f.start(id)

	if err != nil {
		return err
	}

	f.cmd = cmd
	f.errLog = stderr

	f.apipe = apipe
	f.vpipe = vpipe
	f.opipe = opipe

	return nil
}

func (f *Ffmpeg) Wait() error {
	return f.cmd.Wait()
}

func (f *Ffmpeg) AudioWriter() io.WriteCloser {
	return f.apipe
}

func (f *Ffmpeg) VideoWriter() io.WriteCloser {
	return f.vpipe
}

func (f *Ffmpeg) OutputReader() io.WriteCloser {
	return f.opipe
}
