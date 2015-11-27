package main

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/gosuri/uiprogress"
	"github.com/gosuri/uiprogress/util/strutil"

	"gopkg.in/kothar/go-backblaze.v0"
)

// TODO support subdirectories
// TODO support destination path
// TODO support version id downloads
type Get struct {
	Threads int `short:"j" long:"threads" default:"5" description:"Maximum simultaneous downloads to process"`
}

func init() {
	parser.AddCommand("get", "Download a file",
		"Downloads one or more files to the current directory. Specify the bucket with -b, and the filenames to download as extra arguments.",
		&Get{})
}

func (o *Get) Execute(args []string) error {
	client, err := Client()
	if err != nil {
		return err
	}

	bucket, err := client.Bucket(opts.Bucket)
	if err != nil {
		return err
	}
	if bucket == nil {
		return errors.New("Bucket not found: " + opts.Bucket)
	}

	uiprogress.Start()
	fmt.Printf("Making a pool for %d downloads\n", o.Threads)
	pool := make(chan bool, o.Threads)
	group := sync.WaitGroup{}
	var downloadError error

	for _, file := range args {
		// TODO handle wildcards

		fileInfo, reader, err := bucket.DownloadFileByName(file)
		if err != nil {
			downloadError = err
			break
		}

		// Get a ticket to process a download
		pool <- true

		if downloadError != nil {
			break
		}

		group.Add(1)
		go func() {
			defer func() {
				// Allow next entry into pool
				group.Done()
				<-pool
			}()

			err := download(fileInfo, reader, file)
			if err != nil {
				fmt.Println(err)
				downloadError = err
			}
		}()
	}

	group.Wait()

	return downloadError
}

type progressWriter struct {
	bar *uiprogress.Bar
	w   io.Writer
}

func (p *progressWriter) Write(b []byte) (int, error) {
	written, err := p.w.Write(b)
	p.bar.Set(p.bar.Current() + written)
	return written, err
}

func download(fileInfo *backblaze.File, reader io.ReadCloser, path string) error {
	defer reader.Close()

	// Display download progress
	bar := uiprogress.AddBar(int(fileInfo.ContentLength))
	bar.AppendFunc(func(b *uiprogress.Bar) string {
		speed := (float32(b.Current()) / 1024) / float32(b.TimeElapsed().Seconds())
		return fmt.Sprintf("%7.2f KB/s", speed)
	})
	bar.AppendCompleted()
	bar.PrependFunc(func(b *uiprogress.Bar) string { return fmt.Sprintf("%10d", b.Total) })
	bar.PrependFunc(func(b *uiprogress.Bar) string { return strutil.Resize(fileInfo.Name, 50) })
	bar.Width = 20

	err := os.MkdirAll(filepath.Dir(path), 0777)
	if err != nil {
		return err
	}

	writer, err := os.Create(path)
	if err != nil {
		return err
	}
	defer writer.Close()

	sha := sha1.New()
	tee := io.MultiWriter(sha, &progressWriter{bar, writer})

	_, err = io.Copy(tee, reader)
	if err != nil {
		return err
	}

	// Check SHA
	sha1Hash := hex.EncodeToString(sha.Sum(nil))
	if sha1Hash != fileInfo.ContentSha1 {
		return errors.New("Downloaded data does not match SHA1 hash")
	}

	return nil
}
