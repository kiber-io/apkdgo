package sources

import (
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/vbauerster/mpb"
)

type Source interface {
	Name() string
	FindLatestVersion(packageName string) (Version, error)
	Download(version Version) (io.ReadCloser, error)
}

type Version struct {
	Name        string
	Code        int64
	Size        int64
	Link        string
	PackageName string
}

type ProgressReader struct {
	Reader   io.Reader
	Progress *mpb.Bar
}

func (pr *ProgressReader) Read(p []byte) (int, error) {
	n, err := pr.Reader.Read(p)
	pr.Progress.IncrBy(n)
	return n, err
}

var sources = make(map[string]Source)

func Register(d Source) {
	if _, exists := sources[d.Name()]; exists {
		fmt.Fprintf(os.Stderr, "Source %s is already registered!\n", d.Name())
		os.Exit(1)
	}
	if d.Name() != strings.ToLower(d.Name()) {
		fmt.Fprintf(os.Stderr, "Source name %s should be lowercase!\n", d.Name())
		os.Exit(1)
	}
	sources[d.Name()] = d
}

func GetAll() map[string]Source {
	return sources
}

func readBody(res *http.Response) ([]byte, error) {
	var reader io.ReadCloser
	var err error
	switch res.Header.Get("Content-Encoding") {
	case "gzip":
		reader, err = gzip.NewReader(res.Body)
		if err != nil {
			return nil, err
		}
		defer reader.Close()
	default:
		reader = res.Body
	}
	body, err := io.ReadAll(reader)
	return body, err
}

func downloadFile(link string) (io.ReadCloser, error) {
	// outFile, err := os.Create(outputFile)
	// if err != nil {
	// 	return nil, err
	// }
	// defer outFile.Close()
	resp, err := http.Get(link)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
	// defer resp.Body.Close()
	// progressReader := &ProgressReader{
	// 	Reader:   resp.Body,
	// 	Progress: bar,
	// }
	// _, err = io.Copy(outFile, progressReader)
	// if err != nil {
	// 	return nil, err
	// }
	// return os.Open(outputFile)
}
