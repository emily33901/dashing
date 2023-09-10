package main

import (
	"bufio"
	"bytes"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	DashClientGroupSize = 64
)

type DashClient struct {
	id          string
	audioFormat DashFormat
	videoFormat DashFormat
	client      http.Client
	ffmpeg      *Ffmpeg
	output      io.Writer
}

func NewDashClient(id string, af, vf DashFormat, out io.Writer) DashClient {
	return DashClient{
		id:          id,
		audioFormat: af,
		videoFormat: vf,
		output:      out,
	}
}

func makeBackoff(interval time.Duration, initial time.Duration) time.Duration {
	return initial + time.Duration(rand.Int63n(int64(interval)))
}

const (
	SegmentTypeUnknown  = 0
	SegmentTypeNormal   = iota
	SegmentTypeFailed   = iota
	SegmentTypeFinished = iota
)

func (c *DashClient) segmentProducer(
	format DashFormat,
	which string,
	start int,
	pipe io.Writer,
) {
	segmentDuration, err := format.SegmentDuration()
	if err != nil {
		log.Errorln("Unable to get segment duration, assuming 1s")
		segmentDuration = time.Second
	}

	// NOTE(emily): The thought here is that whilst sequentially downloading all these segments takes a long time,
	// for us its not necessarily required to download them sequentially, we could batch several segments to
	// download in parallel and it would work just as well for us.

	type finishedSegment struct {
		seq  int
		typ  int
		data []byte
	}

	finished := make(chan finishedSegment, DashClientGroupSize)

	doSegment := func(sequence int) {
		logger := log.WithFields(log.Fields{
			"which":    which,
			"sequence": sequence,
		})
		lastInfo := ServerInfo{}
		info := ServerInfo{}
		try := 0
		for {
			// Update last with cur
			lastInfo = info

			// Get this sequence's segment url
			segmentUrl, err := format.SegmentURLForSequence(sequence)
			if err != nil {
				// We failed
				// TODO(emily): This also means that other calls to this will probably fail...
				// maybe this should be SegmentTypeFinished instead of just SegmentTypeFailed
				logger.WithFields(log.Fields{"error": err}).Errorln("Failed to get segment URL for sequence")
				finished <- finishedSegment{seq: sequence, typ: SegmentTypeFailed, data: nil}
			}

			startGet := time.Now()
			logger.Debugln("Requesting segment")
			response, err := c.client.Post(segmentUrl, "", nil)
			if err != nil {
				logger.WithFields(log.Fields{"error": err}).Errorln("Failed client Request")
				time.Sleep(makeBackoff(segmentDuration, segmentDuration))
				continue
			}
			finishGet := time.Now()

			if func() bool {
				defer response.Body.Close()

				// Update the last head seq
				info, err = format.ServerInfo(response.Header)
				if err != nil {
					// Missing a header that we want to have, backoff and try again
					logger.WithFields(log.Fields{"error": err, "header": response.Header}).Errorln("Response was missing required fields from header")
					time.Sleep(makeBackoff(segmentDuration, segmentDuration))
					return false
				}
				logger.WithFields(
					log.Fields{"contentLength": response.ContentLength, "statusCode": response.StatusCode}).Debugln(which, sequence, "Got response")

				lastHeadSeq := lastInfo.HeadSequence
				curHeadSeq := info.HeadSequence

				if response.StatusCode != 200 {
					// NOTE(emily): 404, 401 doesn't necessarily indicate that this is the last sequence in the stream
					// It indicates that a segment doesn't exist yet for this sequence. That segment might appear
					// in the future, if the stream keeps running...
					// Here it would make sense to wait until that deadline (i.e. when our segment should be, vs where
					// we currently are) and try again.
					// In order to make that more effective, it might make sense to keep track of whether the head seq
					// is moving aswell, as that will give us a good indication for if we can early out (i.e. head seq
					// has not moved in 5 * segmentDuration, therefore there is no chance that we are ever going to
					// exist because we are 40 * segmentDuration away from the head of the stream). But also gives us a
					// good indication that we will probably exist if head seq moves along.
					// Of course there is the case that even if head seq moves, we still might not exist in the future,
					// however that is probably fine.

					if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusUnauthorized {
						// This segment isn't available yet, or will never exist.
						const deadlineTry = 5

						// Make sure that the head sequence is actually moving forwards
						// (allow for atleast `deadlineTry` durations worth of slack here, because we are not synced
						// with the servers wall-clock).
						// NOTE(emily): If the curHeadSeq is ahead of this sequence, then don't preemptively fail this
						// segment. This is likely to happen if we are downloading a video that has already finished,
						// rather than a livestream.
						if try > deadlineTry && curHeadSeq == lastHeadSeq && curHeadSeq <= sequence {
							logger.WithFields(
								log.Fields{
									"status":           response.StatusCode,
									"try":              try,
									"lastHeadSequence": lastHeadSeq,
									"curHeadSequence":  curHeadSeq,
								}).Warnln("Head sequence is not moving, failing segment")
							finished <- finishedSegment{seq: sequence, typ: SegmentTypeFailed, data: nil}
							return true
						}

						// Wait for the stream move forwards and then try and get this segment again.
						// Atleast 5 delta
						seqDelta := sequence - curHeadSeq
						if seqDelta <= 0 {
							seqDelta = try + 1
						}

						// Try a few times with just a single segment duration to make sure that the head of the stream
						// is moving forwards
						// Otherwise wait for the deadline
						wait := time.Duration(0)
						if try < deadlineTry {
							wait = makeBackoff(segmentDuration, segmentDuration*time.Duration(2))
						} else {
							// Calculate when this sequence should be available, in addition use a random backoff
							wait = makeBackoff(segmentDuration, segmentDuration*time.Duration(seqDelta))
						}

						logger.WithFields(
							log.Fields{
								"status":           response.StatusCode,
								"try":              try,
								"lastHeadSequence": lastHeadSeq,
								"curHeadSequence":  curHeadSeq,
								"timeout":          wait,
							}).Warnln("Segment not available")

						time.Sleep(wait)

						try += 1
						return false
					}

					logger.WithFields(
						log.Fields{
							"status":           response.StatusCode,
							"try":              try,
							"lastHeadSequence": lastHeadSeq,
							"curHeadSequence":  curHeadSeq,
						}).Warnln("Unknown status code whilst trying to get segment")

					logger.Warnln(which, sequence, "Unknown status: ", response.Status)
					time.Sleep(makeBackoff(segmentDuration, segmentDuration))
					return false
				}

				sequenceNum, err := strconv.Atoi(response.Header.Get("X-Sequence-Num"))
				if err != nil {
					// NOTE(emily): This is pessamistic here. I retry just to make sure that this was the correct
					// sequence that we asked for. In all likelyhood it probably is, however without the X-Sequence-Num
					// header there is no way to make sure.
					logger.WithFields(log.Fields{"header": response.Header, "error": err}).Warnln("response header missing sequence number")
					time.Sleep(makeBackoff(segmentDuration, segmentDuration))
					return false
				}

				if sequenceNum != sequence {
					log.Warnln("Waiting (X-Sequence-Number=", sequenceNum, ")")
					time.Sleep(makeBackoff(segmentDuration, segmentDuration))
					return false
				}

				dlStartTime := time.Now()

				// Now try and read all the data from the repsonse body
				// We do this here because we might be ahead of writing by quite a bit, which might cause the response
				// to expire, and the server to boot us.
				// We also dont care about io.ErrUnexpectedEOF: this often happens for the last segment in the stream,
				// which we dont perticularly care about being short...
				bytes, err := io.ReadAll(response.Body)
				if err != io.ErrUnexpectedEOF && err != nil {
					logger.WithFields(log.Fields{"error": err}).Warn("Failed to download segment")
					time.Sleep(makeBackoff(segmentDuration, segmentDuration))
					return false
				}

				dlEndTime := time.Now()

				finished <- finishedSegment{sequence, SegmentTypeNormal, bytes}

				getTime := finishGet.Sub(startGet)
				dlTime := dlEndTime.Sub(dlStartTime)

				logger.WithFields(log.Fields{"requestTime": getTime, "dlTime": dlTime}).Infoln("Downloaded segment")
				return true
			}() {
				break
			}
		}
	}

	nextSeq := start

	for i := 0; i < DashClientGroupSize; i++ {
		go doSegment(nextSeq)
		// time.Sleep(makeBackoff(segmentDuration, segmentDuration))
		nextSeq += 1
	}

	segments := map[int][]byte{}
	nextSeqToWrite := start

	lastSeq := 0

	// Until we wrote our last segment to FFMPEG
	for lastSeq == 0 || nextSeqToWrite < lastSeq {
		// When a segment finishes, store it, and queue up another segment
		// We use this little buffer here to allow for out of order responses for segments
		// which we can then write in order
		log.WithFields(log.Fields{
			"which":     which,
			"sequence":  nextSeqToWrite,
			"remaining": len(segments),
		}).Info("Waiting for finished segment")
		finishedSegment := <-finished
		if finishedSegment.typ == SegmentTypeFailed {
			// If we got lastSeq for the first time, or this lastSeq
			// is smaller than the previous one then update our lastSeq
			if lastSeq == 0 || finishedSegment.seq < lastSeq {
				lastSeq = finishedSegment.seq
				log.WithFields(log.Fields{
					"which":     which,
					"sequence":  nextSeqToWrite,
					"remaining": len(segments),
					"lastSeq":   lastSeq,
				}).Info("Last sequence")
			}
		} else if finishedSegment.typ == SegmentTypeNormal {
			segments[finishedSegment.seq] = finishedSegment.data
		} else {
			panic("unknown segment typ")
		}

		// Try and write out as many segments as possible in order
		for {
			if s, ok := segments[nextSeqToWrite]; ok {
				delete(segments, nextSeqToWrite)
				log.WithFields(log.Fields{
					"which":     which,
					"sequence":  nextSeqToWrite,
					"remaining": len(segments),
				}).Info("Writing to Ffmpeg")
				_, err := io.Copy(pipe, bufio.NewReader(bytes.NewReader(s)))
				if err != nil {
					log.WithFields(log.Fields{
						"which":     which,
						"sequence":  nextSeqToWrite,
						"remaining": len(segments),
						"error":     err,
					}).Error("Failed to write segment")
				}
				log.WithFields(log.Fields{
					"which":     which,
					"sequence":  nextSeqToWrite,
					"remaining": len(segments),
				}).Infoln("Wrote to Ffmpeg")
				nextSeqToWrite += 1

				// If we wrote a segment, then download more segments
				// If there are still sequences to get
				if lastSeq == 0 || nextSeq < lastSeq {
					go doSegment(nextSeq)
					nextSeq += 1
				}

				continue
			}
			break
		}
	}

	log.WithFields(log.Fields{
		"which":    which,
		"sequence": nextSeqToWrite,
		"last":     lastSeq,
	}).Debugln("done")
}

func (c *DashClient) Start() {
	if c.ffmpeg != nil {
		panic("DashClient already started")
	}

	c.ffmpeg = NewFfmpeg(c.id)

	startingSegment := 0

	err := c.ffmpeg.Start(
		func(pipe io.ReadWriteCloser) {
			c.segmentProducer(c.audioFormat, "apipe", startingSegment, pipe)
		}, func(pipe io.ReadWriteCloser) {
			c.segmentProducer(c.videoFormat, "vpipe", startingSegment, pipe)
		}, func(pipe io.ReadWriteCloser) {
			io.Copy(c.output, bufio.NewReader(pipe))
		},
	)

	if err != nil {
		panic(err)
	}
}

func (c *DashClient) Wait() error {
	return c.ffmpeg.Wait()
}

func (c *DashClient) FfmpegError() *strings.Builder {
	return c.ffmpeg.errLog
}
