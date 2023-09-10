package main

import (
	"fmt"
	"io"
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

func (f *Ffmpeg) start(id string) (cmd *exec.Cmd, apipe, vpipe, opipe io.ReadWriteCloser, stderr *strings.Builder, err error) {
	aname, audioPipeListener, err := f.createPipe(id, "a")
	if err != nil {
		return
	}
	defer audioPipeListener.Close()

	vname, videoPipeListener, err := f.createPipe(id, "v")
	if err != nil {
		return
	}
	defer videoPipeListener.Close()

	oname, outputPipeListener, err := f.createPipe(id, "o")
	if err != nil {
		return
	}
	defer outputPipeListener.Close()

	cmd = exec.Command(
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

	stderr = &strings.Builder{}
	cmd.Stderr = stderr

	type pipeResult struct {
		name string
		pipe io.ReadWriteCloser
		err  error
	}

	pipes := make(chan pipeResult)
	defer close(pipes)

	listenPipe := func(name string, listener net.Listener) {
		pipe, err := listener.Accept()

		pipes <- pipeResult{
			name, pipe, err,
		}
	}

	go listenPipe("apipe", audioPipeListener)
	go listenPipe("vpipe", videoPipeListener)
	go listenPipe("opipe", outputPipeListener)

	err = cmd.Start()
	if err != nil {
		return
	}

	pipesMap := map[string]io.ReadWriteCloser{}

	// Get all our pipes
	for len(pipesMap) != 3 {
		pipeResult := <-pipes
		if pipeResult.err != nil {
			err = pipeResult.err
			return
		}

		pipesMap[pipeResult.name] = pipeResult.pipe
	}

	apipe = pipesMap["apipe"]
	vpipe = pipesMap["vpipe"]
	opipe = pipesMap["opipe"]

	return
}
