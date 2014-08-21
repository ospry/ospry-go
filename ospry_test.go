package ospry

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"flag"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io/ioutil"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

var (
	secretKey = flag.String("secretkey", "", "secret api key")
	publicKey = flag.String("publickey", "", "public api key")
	claiming  = flag.Bool("claiming", false, "indicates whether claiming is enabled or not")
	serverURL = flag.String("serverurl", "https://api.ospry.io/v1", "url to api server")
	insecure  = flag.Bool("insecure", false, "disable SSL cert verification")
)

const testFile = "test-imgs/foo.jpg"

var (
	testBytes []byte
	testMeta  *Metadata
)

func newClient() *Client {
	c := New(*secretKey)
	c.ServerURL = *serverURL
	if *insecure {
		c.HTTPClient = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
		}
	}
	if testBytes == nil {
		var err error
		testBytes, err = ioutil.ReadFile(testFile)
		md, err := c.UploadPublic(testFile, bytes.NewReader(testBytes))
		if err != nil {
			panic(err)
		}
		testMeta = md
	}
	return c
}

func TestUpload(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}
	c := newClient()
	config, format, err := image.DecodeConfig(bytes.NewReader(testBytes))
	if err != nil {
		t.Fatal(err)
	}
	isPrivate := false
upload:
	before := time.Now()
	var md *Metadata
	if isPrivate {
		md, err = c.UploadPrivate(testFile, bytes.NewReader(testBytes))
	} else {
		md, err = c.UploadPublic(testFile, bytes.NewReader(testBytes))
	}
	after := time.Now()
	if err != nil {
		t.Fatal(err)
	}
	if md.Filename != filepath.Base(testFile) {
		t.Fatalf("got %s, want %s", md.Filename, filepath.Base(testFile))
	}
	if md.Format != format {
		t.Fatalf("got %s, want %s", md.Format, format)
	}
	if !md.IsClaimed {
		t.Fatal("got false, want true")
	}
	if md.IsPrivate != isPrivate {
		t.Fatalf("got %t, want %t", md.IsPrivate, isPrivate)
	}
	if md.TimeCreated.Before(before) || md.TimeCreated.After(after) {
		t.Fatalf("got %v, expected time between %v and %v", md.TimeCreated, before, after)
	}
	if md.Height != config.Height {
		t.Fatalf("got %d, expected %d", md.Height, config.Height)
	}
	if md.Width != config.Width {
		t.Fatalf("got %d, expected %d", md.Width, config.Width)
	}
	if md.Size != int64(len(testBytes)) {
		t.Fatalf("got %d, expected %d", md.Size, len(testBytes))
	}
	if !isPrivate {
		isPrivate = true
		goto upload
	}
}

func TestDownload(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}
	c := newClient()
	rc, err := c.Download(testMeta.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, err := ioutil.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(b)) != testMeta.Size {
		t.Fatalf("got %d, want %d", len(b), testMeta.Size)
	}
	if format != testMeta.Format {
		t.Fatalf("got %s, want %s", format, testMeta.Format)
	}
	if config.Height != testMeta.Height {
		t.Fatalf("got %d, want %d", testMeta.Height, config.Height)
	}
	if config.Width != testMeta.Width {
		t.Fatalf("got %d, want %d", testMeta.Width, config.Width)
	}
}

func TestClaiming(t *testing.T) {
	if !*claiming {
		t.Skip("skipping because claiming is not enabled")
	}
	// Upload with public key.
	c := newClient()
	c.Key = *publicKey
	testBytes, err := ioutil.ReadFile(testFile)
	md, err := c.UploadPublic(testFile, bytes.NewReader(testBytes))
	if err != nil {
		t.Fatal(err)
	}
	if md.IsClaimed {
		t.Fatalf("got true, want false")
	}
	// Claim with secret key.
	c.Key = *secretKey
	md, err = c.Claim(md.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !md.IsClaimed {
		t.Fatalf("got false, want true")
	}
}

func TestGetMetadata(t *testing.T) {
	c := newClient()
	md, err := c.GetMetadata(testMeta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(md, testMeta) {
		t.Fatalf("got %v\n         want %v", md, testMeta)
	}
}

func TestPrivacy(t *testing.T) {
	if testMeta.IsPrivate {
		t.Fatal("testMeta should start out public")
	}
	c := newClient()
	md, err := c.MakePrivate(testMeta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !md.IsPrivate {
		t.Fatal("got false, want true")
	}
	md, err = c.MakePublic(testMeta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if md.IsPrivate {
		t.Fatal("got true, want false")
	}
}

func TestFormatURLSigning(t *testing.T) {
	c := newClient()
	imgURL := "http://foo.ospry.io/bar/baz.png"
	encodedURL := url.QueryEscape(imgURL)
	inTime := time.Now()
	encodedInTime := url.QueryEscape(inTime.Format(time.RFC3339Nano))
	h := hmac.New(sha256.New, []byte(*secretKey))
	h.Write([]byte(imgURL + "?timeExpired=" + encodedInTime))
	encodedInSignature := url.QueryEscape(base64.StdEncoding.EncodeToString(h.Sum(nil)))
	in := []string{
		imgURL,
		imgURL + "?format=jpeg&maxHeight=200&maxWidth=200",
		fmt.Sprintf("https://api.ospry.io/?url=%s&timeExpired=%s&signature=%s", encodedURL, encodedInTime, encodedInSignature),
		fmt.Sprintf("https://api.ospry.io/?url=%s&timeExpired=%s&signature=%s&format=gif&maxHeight=300&maxWidth=200", encodedURL, encodedInTime, encodedInSignature),
	}
	wantTime := time.Now().Add(time.Minute)
	encodedWantTime := url.QueryEscape(wantTime.Format(time.RFC3339Nano))
	h = hmac.New(sha256.New, []byte(*secretKey))
	h.Write([]byte(imgURL + "?timeExpired=" + encodedWantTime))
	encodedWantSignature := url.QueryEscape(base64.StdEncoding.EncodeToString(h.Sum(nil)))
	want := []string{
		fmt.Sprintf("https://api.ospry.io/?signature=%s&timeExpired=%s&url=%s", encodedWantSignature, encodedWantTime, encodedURL),
		fmt.Sprintf("https://api.ospry.io/?format=jpeg&maxHeight=200&maxWidth=200&signature=%s&timeExpired=%s&url=%s", encodedWantSignature, encodedWantTime, encodedURL),
		fmt.Sprintf("https://api.ospry.io/?signature=%s&timeExpired=%s&url=%s", encodedWantSignature, encodedWantTime, encodedURL),
		fmt.Sprintf("https://api.ospry.io/?format=gif&maxHeight=300&maxWidth=200&signature=%s&timeExpired=%s&url=%s", encodedWantSignature, encodedWantTime, encodedURL),
	}
	testFormatURL(t, c, in, want, &RenderOpts{
		TimeExpired: wantTime,
	})
}

func TestFormatURLRendering(t *testing.T) {
	c := newClient()
	imgURL := "http://foo.ospry.io/bar/baz.png"
	signedURL, _ := c.FormatURL(imgURL, &RenderOpts{TimeExpired: time.Now()})
	parsed, _ := url.Parse(signedURL)
	q := parsed.Query()
	encodedURL := url.QueryEscape(q.Get("url"))
	encodedTime := url.QueryEscape(q.Get("timeExpired"))
	encodedSignature := url.QueryEscape(q.Get("signature"))
	in := []string{
		imgURL,
		imgURL + "?format=jpeg&maxHeight=200&maxWidth=200",
		fmt.Sprintf("https://api.ospry.io/?url=%s&timeExpired=%s&signature=%s", encodedURL, encodedTime, encodedSignature),
		fmt.Sprintf("https://api.ospry.io/?url=%s&timeExpired=%s&signature=%s&format=gif&maxHeight=200&maxWidth=200", encodedURL, encodedTime, encodedSignature),
	}
	want := []string{
		imgURL + "?format=gif&maxHeight=120&maxWidth=354",
		imgURL + "?format=gif&maxHeight=120&maxWidth=354",
		fmt.Sprintf("https://api.ospry.io/?format=gif&maxHeight=120&maxWidth=354&signature=%s&timeExpired=%s&url=%s", encodedSignature, encodedTime, encodedURL),
		fmt.Sprintf("https://api.ospry.io/?format=gif&maxHeight=120&maxWidth=354&signature=%s&timeExpired=%s&url=%s", encodedSignature, encodedTime, encodedURL),
	}
	testFormatURL(t, c, in, want, &RenderOpts{
		Format:    "gif",
		MaxHeight: 120,
		MaxWidth:  354,
	})
	in = []string{
		imgURL + "?format=gif",
	}
	want = []string{
		imgURL + "?format=gif&maxHeight=350",
	}
	testFormatURL(t, c, in, want, &RenderOpts{
		MaxHeight: 350,
	})
}

func testFormatURL(t *testing.T, c *Client, in, want []string, opts *RenderOpts) {
	for i, v := range in {
		url, err := c.FormatURL(v, opts)
		if err != nil {
			t.Fatal(err)
		}
		if url != want[i] {
			t.Fatalf("got %s\n         want %s", url, want[i])
		}
	}
}
