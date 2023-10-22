package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

func (f *Ffmpeg) createPipeName(id, name string) (path string, err error) {
	file, err := os.CreateTemp("", fmt.Sprintf("dashing.%s.%s.*.pipe", id, name))
	if err != nil {
		return
	}

	path = file.Name()

	err = file.Close()
	err = os.Remove(path)
	return
}

func (f *Ffmpeg) createPipe(id, name string) (pipeName string, file *os.File, err error) {
	pipeName, err = f.createPipeName(id, name)

	if err != nil {
		return
	}

	err = syscall.Mkfifo(pipeName, 0666)

	if err != nil {
		return
	}

	file, err = os.OpenFile(pipeName, os.O_RDWR|os.O_CREATE|os.O_APPEND, os.ModeNamedPipe)

	return
}

func (f *Ffmpeg) start(id string, onAudioPipe, onVideoPipe, onOutputPipe OnPipe) (cmd *exec.Cmd, stderr *strings.Builder, err error) {
	aname, audioFifo, err := f.createPipe(id, "a")
	if err != nil {
		return
	}

	vname, videoFifo, err := f.createPipe(id, "v")
	if err != nil {
		return
	}

	oname, outputFifo, err := f.createPipe(id, "o")
	if err != nil {
		return
	}

	cmd = f.makeCmd(aname, vname, oname)

	stderr = &strings.Builder{}
	cmd.Stderr = stderr

	go func() {
		defer videoFifo.Close()
		onVideoPipe(videoFifo)
	}()
	go func() {
		defer audioFifo.Close()
		onAudioPipe(videoFifo)
	}()
	go func() {
		defer outputFifo.Close()
		onOutputPipe(videoFifo)
	}()

	err = cmd.Start()

	return
}
