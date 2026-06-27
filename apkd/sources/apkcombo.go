package sources

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	neturl "net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/kiber-io/apkd/apkd/network"
	fakeUserAgent "github.com/lib4u/fake-useragent"

	"github.com/PuerkitoBio/goquery"
)

type ApkCombo struct {
	BaseSource
	baseURL string
}

type apkComboVersionItem struct {
	VersionName string
	Link        string
	Type        FileType
	VersionCode int
}

type ApkComboConfig struct {
	BaseSourceConfig `yaml:",inline"`
	BaseURL          string `yaml:"base_url"`
}

func (s *ApkCombo) fetchDocument(url string) (*goquery.Document, *neturl.URL, error) {
	req, err := s.NewRequest("GET", url, nil)
	if err != nil {
		return nil, nil, err
	}
	res, err := s.Http().Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("request failed: %w", err)
	}
	reader, err := unpackResponse(res)
	if err != nil {
		_ = res.Body.Close()
		return nil, nil, err
	}
	defer func() {
		_ = reader.Close()
		if reader != res.Body {
			_ = res.Body.Close()
		}
	}()
	if res.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("error: %s", res.Status)
	}
	doc, err := goquery.NewDocumentFromReader(reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse HTML: %w", err)
	}
	var resolvedURL *neturl.URL
	if res.Request != nil && res.Request.URL != nil {
		resolvedURL = new(neturl.URL)
		*resolvedURL = *res.Request.URL
	}
	return doc, resolvedURL, nil
}

func parseVersionCodeText(rawText string) (int, error) {
	versionCodeText := strings.TrimSpace(rawText)
	if strings.HasPrefix(versionCodeText, "(") && strings.HasSuffix(versionCodeText, ")") && len(versionCodeText) >= 2 {
		versionCodeText = strings.TrimSpace(versionCodeText[1 : len(versionCodeText)-1])
	}
	if versionCodeText == "" {
		return 0, errors.New("version code is empty")
	}
	versionCode, err := strconv.Atoi(versionCodeText)
	if err != nil {
		return 0, fmt.Errorf("invalid version code %q: %w", versionCodeText, err)
	}
	if versionCode <= 0 {
		return 0, fmt.Errorf("invalid version code %q: must be positive", versionCodeText)
	}
	return versionCode, nil
}

func parseApkComboVersionName(rawText string) (string, error) {
	versionNameFull := strings.TrimSpace(rawText)
	versionParts := strings.Fields(versionNameFull)
	if len(versionParts) < 2 {
		return "", fmt.Errorf("invalid version name %q", versionNameFull)
	}
	versionName := versionParts[len(versionParts)-1]
	if versionName == "" {
		return "", fmt.Errorf("invalid version name %q", versionNameFull)
	}
	return versionName, nil
}

func parseApkComboFileType(rawText string) (FileType, error) {
	switch strings.TrimSpace(rawText) {
	case "APK":
		return APK, nil
	case "XAPK":
		return XAPK, nil
	default:
		return "", fmt.Errorf("unknown file type: %q", rawText)
	}
}

func (s *ApkCombo) Name() string {
	return "apkcombo"
}

func (s *ApkCombo) Download(version Version) (*DownloadStream, error) {
	checkin, err := s.checkin(version.Link)
	if err != nil {
		return nil, fmt.Errorf("failed to perform checkin: %w", err)
	}
	downloadLink := version.Link + "&" + checkin
	req, err := s.NewRequest("GET", downloadLink, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req = network.WithCheckRedirect(req, func(req *http.Request, via []*http.Request) error {
		req.Header.Del("Referer")
		return nil
	})
	return createResponseReader(s.Http(), req)
}

func (s *ApkCombo) parseVersionItem(e *goquery.Selection) (apkComboVersionItem, error) {
	verNameBlock := e.Find(".vername")
	if verNameBlock.Length() == 0 {
		return apkComboVersionItem{}, errors.New("version name not found")
	}
	versionName, err := parseApkComboVersionName(verNameBlock.Text())
	if err != nil {
		return apkComboVersionItem{}, err
	}

	typeBlock := e.Find(".vtype")
	if typeBlock.Length() == 0 {
		return apkComboVersionItem{}, errors.New("file type not found")
	}
	fileTypeBlock := typeBlock.Children().First()
	if fileTypeBlock.Length() == 0 {
		return apkComboVersionItem{}, errors.New("file type not found")
	}
	fileType, err := parseApkComboFileType(fileTypeBlock.Text())
	if err != nil {
		return apkComboVersionItem{}, err
	}

	verCodeBlock := e.Find(".vercode").First()
	if verCodeBlock.Length() == 0 {
		return apkComboVersionItem{}, fmt.Errorf("version code not found")
	}
	versionCode, err := parseVersionCodeText(verCodeBlock.Text())
	if err != nil {
		return apkComboVersionItem{}, err
	}

	link, exists := e.Attr("href")
	if !exists {
		return apkComboVersionItem{}, errors.New("download link not found")
	}

	return apkComboVersionItem{
		VersionName: versionName,
		Link:        link,
		Type:        fileType,
		VersionCode: versionCode,
	}, nil
}

func (s *ApkCombo) resolveVersionCode(versionUrl string) (apkComboVersionItem, error) {
	doc, _, err := s.fetchDocument(versionUrl)
	if err != nil {
		return apkComboVersionItem{}, err
	}
	variantBlock := doc.Find("#variants-tab .variant")
	if variantBlock.Length() == 0 {
		variantBlock = doc.Find("#best-variant-tab .variant")
		if variantBlock.Length() == 0 {
			return apkComboVersionItem{}, fmt.Errorf("no variants found for version URL %s", versionUrl)
		}
	}
	versionCandidate, err := s.parseVersionItem(variantBlock)
	if err != nil {
		return apkComboVersionItem{}, err
	}
	return versionCandidate, nil
}

func sleepWithJitter(base time.Duration, maxJitter time.Duration) {
	jitter := time.Duration(rand.Int63n(int64(maxJitter)))
	time.Sleep(base + jitter)
}

func (s *ApkCombo) tryToFindOldVersion(link string, versionCode int) (apkComboVersionItem, error) {
	doc, _, err := s.fetchDocument(link)
	if err != nil {
		return apkComboVersionItem{}, err
	}
	var foundVersion apkComboVersionItem
	doc.Find(".ver-item").EachWithBreak(func(i int, e *goquery.Selection) bool {
		versionLink, exists := e.Attr("href")
		if !exists {
			s.Log().Logw("Version item missing href attribute")
			return true
		}
		if versionLink == "" {
			s.Log().Logw("Version item has empty href attribute")
			return true
		}
		versionLink, err = neturl.JoinPath(s.baseURL, versionLink)
		if err != nil {
			s.Log().Logw(fmt.Sprintf("Failed to join version link path: %v", err))
			return true
		}
		sleepWithJitter(200*time.Millisecond, 200*time.Millisecond)
		versionItem, err := s.resolveVersionCode(versionLink)
		if err != nil {
			s.Log().Logw(fmt.Sprintf("Failed to parse version item: %v", err))
			return true
		}
		if versionItem.VersionCode != versionCode {
			return true
		}
		foundVersion = versionItem
		return false
	})
	if foundVersion.VersionCode == 0 {
		lastBtn := doc.Find(".pagination .buttons .button:last-child")
		_, disabled := lastBtn.Attr("disabled")
		if !disabled {
			href, exists := lastBtn.Attr("href")
			if !exists {
				s.Log().Logw("Last pagination button missing href attribute")
			} else {
				base, _ := neturl.Parse(s.baseURL)
				ref, err := neturl.Parse(href)
				if err != nil {
					s.Log().Logw(fmt.Sprintf("Failed to parse next page URL: %v", err))
				} else {
					sleepWithJitter(200*time.Millisecond, 200*time.Millisecond)
					return s.tryToFindOldVersion(base.ResolveReference(ref).String(), versionCode)
				}
			}
		}
		return apkComboVersionItem{}, fmt.Errorf("version code %d not found", versionCode)
	}
	return foundVersion, nil
}

func (s *ApkCombo) checkin(referer string) (string, error) {
	url := fmt.Sprintf("%s/checkin", s.baseURL)
	req, err := s.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Referer", referer)
	res, err := s.Http().Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	reader, err := unpackResponse(res)
	if err != nil {
		_ = res.Body.Close()
		return "", err
	}
	defer func() {
		_ = reader.Close()
		if reader != res.Body {
			_ = res.Body.Close()
		}
	}()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("error: %s", res.Status)
	}
	bodyBytes, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}
	return string(bodyBytes), nil
}

func (s *ApkCombo) FindByPackage(packageName string, versionCode int) (Version, error) {
	var version Version

	packageURL := fmt.Sprintf("%s/search/?q=%s", s.baseURL, packageName)
	doc, resolvedPackageURL, err := s.fetchDocument(packageURL)
	if err != nil {
		return version, err
	}
	if resolvedPackageURL == nil {
		return version, errors.New("failed to resolve package URL")
	}
	packagePageUrl := resolvedPackageURL.String()

	if doc.Find(".app_header").Length() == 0 {
		return version, &AppNotFoundError{PackageName: packageName}
	}

	var authorName string
	authorBlock := doc.Find(".author .is-link")
	if authorBlock.Length() == 0 {
		s.Log().Logw(fmt.Sprintf("Author not found for package %s", packageName))
	} else {
		authorName = strings.TrimSpace(authorBlock.Text())
	}
	version.DeveloperId = authorName

	var versionItem apkComboVersionItem
	if versionCode != 0 {
		oldVersionUrl, err := neturl.JoinPath(packagePageUrl, "old-versions")
		if err != nil {
			return version, err
		}
		versionItem, err = s.tryToFindOldVersion(oldVersionUrl, versionCode)
		if err != nil {
			return version, err
		}
	} else {
		downloadPageUrl, err := neturl.JoinPath(packagePageUrl, "download/apk")
		if err != nil {
			return version, err
		}
		versionItem, err = s.resolveVersionCode(downloadPageUrl)
		if err != nil {
			return version, err
		}
	}
	if versionItem.VersionCode == 0 {
		return version, &AppNotFoundError{PackageName: packageName}
	}
	version.Code = versionItem.VersionCode
	version.Name = versionItem.VersionName
	version.Link = versionItem.Link
	version.Type = versionItem.Type
	version.PackageName = packageName

	if version.Code == 0 {
		return version, &AppNotFoundError{PackageName: packageName}
	}

	return version, nil
}

func (s *ApkCombo) FindByDeveloper(developerId string) ([]string, error) {
	var packages []string

	url := fmt.Sprintf("%s/developer/%s", s.baseURL, developerId)
	req, err := s.NewRequest("GET", url, nil)
	if err != nil {
		return packages, err
	}
	res, err := s.Net.Do(req)
	if err != nil {
		return packages, fmt.Errorf("request failed: %w", err)
	}
	defer res.Body.Close()
	reader, err := unpackResponse(res)
	if err != nil {
		return packages, err
	}
	defer reader.Close()
	doc, err := goquery.NewDocumentFromReader(reader)
	if err != nil {
		return packages, fmt.Errorf("failed to parse developer page HTML: %w", err)
	}

	doc.Find(".l_item").EachWithBreak(func(i int, e *goquery.Selection) bool {
		link, exists := e.Attr("href")
		if !exists {
			err = errors.New("link attribute not found")
			return false
		}
		link, err = neturl.JoinPath(s.baseURL, link)
		if err != nil {
			err = fmt.Errorf("failed to join path: %w", err)
			return false
		}
		packageName := path.Base(link)
		packages = append(packages, packageName)
		return true
	})
	if err != nil {
		packages = nil
	}

	return packages, err
}

func defaultApkComboConfig() ApkComboConfig {
	return ApkComboConfig{
		BaseURL: "https://apkcombo.com",
	}
}

func newApkComboSource() (Source, error) {
	s := &ApkCombo{}
	s.Source = s
	ua, err := fakeUserAgent.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create fake user agent: %w", err)
	}
	config, err := ResolveSourceConfig(s.Name(), defaultApkComboConfig())
	if err != nil {
		return nil, fmt.Errorf("failed to decode apkcombo config: %w", err)
	}
	s.baseURL = config.BaseURL
	randomUA := ua.Filter().Platform(fakeUserAgent.Desktop).Browser(fakeUserAgent.Firefox, fakeUserAgent.Chrome).Get()
	s.Log().Logd("Using User-Agent: " + randomUA)
	headers := ApplyConfiguredHeaders(http.Header{
		"User-Agent":                {randomUA},
		"sec-gpc":                   {"1"},
		"upgrade-insecure-requests": {"1"},
		"sec-fetch-dest":            {"document"},
		"sec-fetch-mode":            {"navigate"},
		"sec-fetch-site":            {"none"},
		"sec-fetch-user":            {"?1"},
		"priority":                  {"u=0, i"},
		"te":                        {"trailers"},
	}, config.Headers)
	s.Net = network.DefaultClientForSource(s.Name()).WithDefaultHeaders(headers)
	return s, nil
}

func init() {
	RegisterSourceFactoryWithConfig(newApkComboSource, "apkcombo", NewConfigDecoderWithDefaults(
		ApkComboConfig{},
		func(c *ApkComboConfig) {
			NormalizeBaseSourceConfig(&c.BaseSourceConfig)
		},
		func(c ApkComboConfig) error {
			return ValidateBaseSourceConfig(c.BaseSourceConfig)
		},
	))
}
