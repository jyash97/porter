package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/pkg/archive"
	"github.com/moby/moby/pkg/jsonmessage"
	"github.com/moby/moby/pkg/stringid"
	"github.com/moby/term"
	"github.com/pkg/errors"
)

type BuildOpts struct {
	ImageRepo         string
	Tag               string
	BuildContext      string
	DockerfilePath    string
	IsDockerfileInCtx bool

	Env map[string]string
}

// BuildLocal
func (a *Agent) BuildLocal(opts *BuildOpts) error {
	dockerfilePath := opts.DockerfilePath
	tar, err := archive.TarWithOptions(opts.BuildContext, &archive.TarOptions{})

	if err != nil {
		return err
	}

	if !opts.IsDockerfileInCtx {
		dockerfileCtx, err := os.Open(dockerfilePath)

		if err != nil {
			return errors.Errorf("unable to open Dockerfile: %v", err)
		}

		defer dockerfileCtx.Close()

		// add the dockerfile to the build context
		tar, dockerfilePath, err = AddDockerfileToBuildContext(dockerfileCtx, tar)

		if err != nil {
			return err
		}
	}

	buildArgs := make(map[string]*string)

	for key, val := range opts.Env {
		valCopy := val
		buildArgs[key] = &valCopy
	}

	out, err := a.client.ImageBuild(context.Background(), tar, types.ImageBuildOptions{
		Dockerfile: dockerfilePath,
		BuildArgs:  buildArgs,
		Tags: []string{
			fmt.Sprintf("%s:%s", opts.ImageRepo, opts.Tag),
		},
		Remove: true,
	})

	if err != nil {
		return err
	}

	defer out.Body.Close()

	termFd, isTerm := term.GetFdInfo(os.Stderr)

	return jsonmessage.DisplayJSONMessagesStream(out.Body, os.Stderr, termFd, isTerm, nil)
}

// AddDockerfileToBuildContext from a ReadCloser, returns a new archive and
// the relative path to the dockerfile in the context.
func AddDockerfileToBuildContext(dockerfileCtx io.ReadCloser, buildCtx io.ReadCloser) (io.ReadCloser, string, error) {
	file, err := ioutil.ReadAll(dockerfileCtx)
	dockerfileCtx.Close()
	if err != nil {
		return nil, "", err
	}
	now := time.Now()
	hdrTmpl := &tar.Header{
		Mode:       0600,
		Uid:        0,
		Gid:        0,
		ModTime:    now,
		Typeflag:   tar.TypeReg,
		AccessTime: now,
		ChangeTime: now,
	}
	randomName := ".dockerfile." + stringid.GenerateRandomID()[:20]

	buildCtx = archive.ReplaceFileTarWrapper(buildCtx, map[string]archive.TarModifierFunc{
		// Add the dockerfile with a random filename
		randomName: func(_ string, h *tar.Header, content io.Reader) (*tar.Header, []byte, error) {
			return hdrTmpl, file, nil
		},
		// Update .dockerignore to include the random filename
		".dockerignore": func(_ string, h *tar.Header, content io.Reader) (*tar.Header, []byte, error) {
			if h == nil {
				h = hdrTmpl
			}

			b := &bytes.Buffer{}
			if content != nil {
				if _, err := b.ReadFrom(content); err != nil {
					return nil, nil, err
				}
			} else {
				b.WriteString(".dockerignore")
			}
			b.WriteString("\n" + randomName + "\n")
			return h, b.Bytes(), nil
		},
	})
	return buildCtx, randomName, nil
}
