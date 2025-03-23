package sources

import (
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strconv"
	"strings"

	"kiber-io/apkd/apkd/browsers"

	"github.com/PuerkitoBio/goquery"
	"github.com/dustin/go-humanize"
)

type ApkCombo struct {
	BaseSource
}

func (s ApkCombo) Name() string {
	return "apkcombo"
}
func (s ApkCombo) Download(version Version) (io.ReadCloser, error) {
	return downloadFile(version.Link)
}

func (s ApkCombo) FindByPackage(packageName string, versionCode int) (Version, error) {
	var version Version

	url := "https://apkcombo.com/search?q=" + packageName

	req, err := http.NewRequest("GET", url, nil)

	if err != nil {
		return version, err
	}

	req.Header.Add("User-Agent", browsers.GetRandomUserAgent())
	req.Header.Add("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Add("Accept-Encoding", "gzip")
	req.Header.Add("accept-language", "en-US")
	req.Header.Add("dnt", "1")
	req.Header.Add("upgrade-insecure-requests", "1")
	req.Header.Add("sec-fetch-dest", "document")
	req.Header.Add("sec-fetch-mode", "navigate")
	req.Header.Add("sec-fetch-site", "same-origin")
	req.Header.Add("sec-fetch-user", "?1")
	req.Header.Add("priority", "u=0, i")
	req.Header.Add("te", "trailers")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return version, err
	}
	defer res.Body.Close()
	reader, err := unpackResponse(res)
	if err != nil {
		return version, err
	}
	defer reader.Close()
	doc, err := goquery.NewDocumentFromReader(reader)
	if err != nil {
		return version, err
	}

	authorBlock := doc.Find(".author .is-link")
	if authorBlock.Length() == 0 {
		return version, fmt.Errorf("author not found")
	}
	authorName := strings.TrimSpace(authorBlock.Text())

	url, err = neturl.JoinPath(res.Request.URL.String(), "old-versions")
	if err != nil {
		return version, err
	}
	req, err = http.NewRequest("GET", url, nil)
	if err != nil {
		return version, err
	}

	res, err = http.DefaultClient.Do(req)
	if err != nil {
		return version, err
	}
	reader, err = unpackResponse(res)
	if err != nil {
		return version, err
	}
	defer reader.Close()
	doc, err = goquery.NewDocumentFromReader(reader)
	if err != nil {
		return version, err
	}
	doc.Find(".ver-item").EachWithBreak(func(i int, s *goquery.Selection) bool {
		link, exists := s.Attr("href")
		if !exists {
			return true
		}
		linkUrl, err := neturl.Parse(link)
		if err != nil {
			return true
		}
		link = res.Request.URL.ResolveReference(linkUrl).String()
		req, err = http.NewRequest("GET", link, nil)
		if err != nil {
			return true
		}
		res, err = http.DefaultClient.Do(req)
		if err != nil {
			return true
		}
		reader, err = unpackResponse(res)
		if err != nil {
			return true
		}
		defer reader.Close()
		versionDoc, err := goquery.NewDocumentFromReader(reader)
		if err != nil {
			return true
		}
		versionBlocks := versionDoc.Find(".variant")
		if versionBlocks.Length() == 0 {
			return true
		}
		versionBlock := versionBlocks.First()
		vercodeBlock := versionBlock.Find(".vercode")
		if vercodeBlock.Length() == 0 {
			return true
		}
		versionCodeRemoteText := strings.TrimSpace(vercodeBlock.Text())
		// remove brackets
		versionCodeRemoteText = versionCodeRemoteText[1 : len(versionCodeRemoteText)-1]
		versionCodeRemote, err := strconv.Atoi(versionCodeRemoteText)
		if err != nil {
			return true
		}
		if versionCode != 0 && versionCodeRemote != versionCode {
			return true
		}
		vernameBlock := versionBlock.Find(".vername")
		if vernameBlock.Length() == 0 {
			return true
		}
		versionNameFull := vernameBlock.Text()
		versionParts := strings.Split(versionNameFull, " ")
		if len(versionParts) < 2 {
			return true
		}
		versionName := versionParts[len(versionParts)-1]
		link, exists = versionBlock.Attr("href")
		if !exists {
			return true
		}
		linkUrl, err = neturl.Parse(link)
		if err != nil {
			return true
		}
		link = res.Request.URL.ResolveReference(linkUrl).String()

		sizeBlock := versionBlock.Find(".description .spec.ltr")
		if sizeBlock.Length() == 0 {
			return true
		}
		sizeText := strings.TrimSpace(sizeBlock.Text())
		size, err := humanize.ParseBytes(sizeText)
		if err != nil {
			return true
		}
		vTypeBlock := versionBlock.Find(".vtype")
		if vTypeBlock.Length() == 0 {
			return true
		}
		var fileType FileType
		fileTypeBlock := vTypeBlock.Children()
		if fileTypeBlock.Length() == 0 {
			return true
		}
		fileTypeText := strings.TrimSpace(fileTypeBlock.Text())
		if fileTypeText == "APK" {
			fileType = APK
		} else if fileTypeText == "XAPK" {
			fileType = XAPK
		}
		if fileType == "" {
			return true
		}

		version.Code = versionCodeRemote
		version.Name = versionName
		version.Link = link
		version.Size = size
		version.PackageName = packageName
		version.DeveloperId = authorName
		version.Type = fileType
		return false
	})

	if version.Code == 0 {
		return version, &AppNotFoundError{PackageName: packageName}
	}

	return version, nil
}

func init() {
	s := ApkCombo{}
	Register(s)
}
