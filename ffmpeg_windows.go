package main

import (
	"fmt"
	"net"
	"os/exec"
	"strings"

	"gopkg.in/natefinch/npipe.v2"
)

func (f *Ffmpeg) createPipeName(id, name string) string {
	pipeName := fmt.Sprintf(`\\.\pipe\DashClient\%s\%s`, id, name)
	return pipeName
}

func (f *Ffmpeg) createPipe(id, name string) (pipeName string, pipe net.Listener, err error) {
	pipeName = f.createPipeName(id, name)
	pipe, err = npipe.Listen(pipeName)

	return
}

func (f *Ffmpeg) start(id string, onAudioPipe, onVideoPipe, onOutputPipe OnPipe) (cmd *exec.Cmd, stderr *strings.Builder, err error) {
	aname, audioPipeListener, err := f.createPipe(id, "a")
	if err != nil {
		return
	}

	vname, videoPipeListener, err := f.createPipe(id, "v")
	if err != nil {
		return
	}

	oname, outputPipeListener, err := f.createPipe(id, "o")
	if err != nil {
		return
	}

	cmd = f.makeCmd(aname, vname, oname)

	stderr = &strings.Builder{}
	cmd.Stderr = stderr

	go func() {
		defer videoPipeListener.Close()
		pipe, err := videoPipeListener.Accept()

		if err != nil {
			panic(err)
		}
		defer pipe.Close()

		onVideoPipe(pipe)
	}()

	go func() {
		defer audioPipeListener.Close()
		pipe, err := audioPipeListener.Accept()

		if err != nil {
			panic(err)
		}
		defer pipe.Close()

		onAudioPipe(pipe)
	}()

	go func() {
		defer outputPipeListener.Close()
		pipe, err := outputPipeListener.Accept()

		if err != nil {
			panic(err)
		}
		defer pipe.Close()

		onOutputPipe(pipe)
	}()

	err = cmd.Start()

	return
}
