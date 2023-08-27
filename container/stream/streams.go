package stream // import "github.com/docker/docker/container/stream"

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"

	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/log"
	"github.com/docker/docker/pkg/broadcaster"
	"github.com/docker/docker/pkg/ioutils"
	"github.com/docker/docker/pkg/pools"
)

// Config holds information about I/O streams managed together.
//
// config.StdinPipe returns a WriteCloser which can be used to feed data
// to the standard input of the streamConfig's active process.
// config.StdoutPipe and streamConfig.StderrPipe each return a ReadCloser
// which can be used to retrieve the standard output (and error) generated
// by the container's active process. The output (and error) are actually
// copied and delivered to all StdoutPipe and StderrPipe consumers, using
// a kind of "broadcaster".
type Config struct {
	wg        sync.WaitGroup
	stdout    *broadcaster.Unbuffered
	stderr    *broadcaster.Unbuffered
	stdin     io.ReadCloser
	stdinPipe io.WriteCloser
	dio       *cio.DirectIO
}

// NewConfig creates a stream config and initializes
// the standard err and standard out to new unbuffered broadcasters.
func NewConfig() *Config {
	return &Config{
		stderr: new(broadcaster.Unbuffered),
		stdout: new(broadcaster.Unbuffered),
	}
}

// Stdout returns the standard output in the configuration.
func (c *Config) Stdout() *broadcaster.Unbuffered {
	return c.stdout
}

// Stderr returns the standard error in the configuration.
func (c *Config) Stderr() *broadcaster.Unbuffered {
	return c.stderr
}

// Stdin returns the standard input in the configuration.
func (c *Config) Stdin() io.ReadCloser {
	return c.stdin
}

// StdinPipe returns an input writer pipe as an io.WriteCloser.
func (c *Config) StdinPipe() io.WriteCloser {
	return c.stdinPipe
}

// StdoutPipe creates a new io.ReadCloser with an empty bytes pipe.
// It adds this new out pipe to the Stdout broadcaster.
// This will block stdout if unconsumed.
func (c *Config) StdoutPipe() io.ReadCloser {
	bytesPipe := ioutils.NewBytesPipe()
	c.stdout.Add(bytesPipe)
	return bytesPipe
}

// StderrPipe creates a new io.ReadCloser with an empty bytes pipe.
// It adds this new err pipe to the Stderr broadcaster.
// This will block stderr if unconsumed.
func (c *Config) StderrPipe() io.ReadCloser {
	bytesPipe := ioutils.NewBytesPipe()
	c.stderr.Add(bytesPipe)
	return bytesPipe
}

// NewInputPipes creates new pipes for both standard inputs, Stdin and StdinPipe.
func (c *Config) NewInputPipes() {
	c.stdin, c.stdinPipe = io.Pipe()
}

// NewNopInputPipe creates a new input pipe that will silently drop all messages in the input.
func (c *Config) NewNopInputPipe() {
	c.stdinPipe = ioutils.NopWriteCloser(io.Discard)
}

// CloseStreams ensures that the configured streams are properly closed.
func (c *Config) CloseStreams() error {
	var errors []string

	if c.stdin != nil {
		if err := c.stdin.Close(); err != nil {
			errors = append(errors, fmt.Sprintf("error close stdin: %s", err))
		}
	}

	if err := c.stdout.Clean(); err != nil {
		errors = append(errors, fmt.Sprintf("error close stdout: %s", err))
	}

	if err := c.stderr.Clean(); err != nil {
		errors = append(errors, fmt.Sprintf("error close stderr: %s", err))
	}

	if len(errors) > 0 {
		return fmt.Errorf(strings.Join(errors, "\n"))
	}

	return nil
}

type Transformation struct {
	Pattern     *regexp.Regexp
	Replacement string
}

func applyTransformations(s string, transformations []Transformation) string {
	for _, t := range transformations {
		s = t.Pattern.ReplaceAllString(s, t.Replacement)
	}
	return s
}

// TransformWriter wraps an io.Writer and applies transformations to the data being written.
type TransformWriter struct {
	w               io.Writer
	transformations []Transformation
	buffer          []byte
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (tw *TransformWriter) Write(p []byte) (n int, err error) {
	// Append the previous buffer to the current payload
	payload := append(tw.buffer, p...)

	transformed := applyTransformations(string(payload), tw.transformations)

	// Store the last few bytes to the buffer for the next Write call
	tw.buffer = payload[max(0, len(payload)-maxTransformLength):]

	n, err = tw.w.Write([]byte(transformed))
	return n - len(tw.buffer), err
}

const maxTransformLength = 100 // Adjust based on the maximum expected length of a transformation pattern

func (c *Config) CopyToPipe(iop *cio.DirectIO) {
	ctx := context.TODO()

	c.dio = iop
	copyFunc := func(w io.Writer, r io.ReadCloser, transformations []Transformation) {
		tw := &TransformWriter{w: w, transformations: transformations}
		c.wg.Add(1)
		go func() {
			if _, err := pools.Copy(tw, r); err != nil {
				log.G(ctx).Errorf("stream copy error: %v", err)
			}
			r.Close()
			c.wg.Done()
		}()
	}

	stdoutTransforms := []Transformation{
		{Pattern: regexp.MustCompile("{black}"), Replacement: "{white}"},
	}
	stderrTransforms := []Transformation{
		{Pattern: regexp.MustCompile("{red}"), Replacement: "{grn}"},
	}
	// stdinTransforms := []Transformation{
	// 	{Pattern: regexp.MustCompile("orange"), Replacement: "blue"},
	// }

	if iop.Stdout != nil {
		copyFunc(c.Stdout(), iop.Stdout, stdoutTransforms)
	}
	if iop.Stderr != nil {
		copyFunc(c.Stderr(), iop.Stderr, stderrTransforms)
	}
	if stdin := c.Stdin(); stdin != nil {
		if iop.Stdin != nil {
			go func() {
				// tw := &TransformWriter{w: iop.Stdin, transformations: stdinTransforms}
				// pools.Copy(tw, stdin)
				pools.Copy(iop.Stdin, stdin)
				if err := iop.Stdin.Close(); err != nil {
					log.G(ctx).Warnf("failed to close stdin: %v", err)
				}
			}()
		}
	}
}

// Wait for the stream to close
// Wait supports timeouts via the context to unblock and forcefully
// close the io streams
func (c *Config) Wait(ctx context.Context) {
	done := make(chan struct{}, 1)
	go func() {
		c.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		if c.dio != nil {
			c.dio.Cancel()
			c.dio.Wait()
			c.dio.Close()
		}
	}
}
