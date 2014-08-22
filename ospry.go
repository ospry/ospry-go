// Package ospry provides bindings for ospry's image hosting api (see
// https://ospry.io).
//
// If you're writing a web service, images will generally be uploaded
// directly to ospry from the browser via ospry.js (with your public
// key), but you'll keep track of them server-side. Server-side
// operations should be done with your secret key:
//
//   ospry.SetKey("sk-test-********")
//
// If you've turned the claiming feature on in your account settings
// (recommended), then you'll need to claim the images after your
// client uploads them and sends the resulting ids to your server:
//
//   metadata, err := ospry.Claim(id)
//
// Once you have claimed the images, you can retrieve their metadata,
// change their permissions and delete them as needed.
//
//   metadata, err := ospry.GetMetadata(id)
//   metadata, err := ospry.MakePrivate(id)
//   metadata, err := ospry.MakePublic(id)
//   err := ospry.Delete(id)
//
// To give access to private images to someone that doesn't have your
// secret key (i.e your js client running in the browser), you can use
// FormatURL to sign the urls by providing an expiration time.
//
//   url, err := ospry.FormatURL(image.URL, &RenderOpts{
//     TimeExpired: time.Now().Add(5*time.Minute),
//   })
//
// Image data can be uploaded and downloaded server-side too if you
// want:
//
//   metadata, err := ospry.UploadPublic("foo.jpg", fooReader)
//   metadata, err := ospry.UploadPrivate("bar.jpg", barReader)
//   readCloser, err := ospry.Download(metadata.URL, &RenderOpts{MaxHeight: 400})
//
// Remember to close any ReadClosers you get from Download once you're
// done reading.
//
package ospry

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

var (
	Formats       = []string{"jpeg", "png", "gif"}
	DefaultClient = New("")
)

type Metadata struct {
	ID          string    `json:"id"`
	URL         string    `json:"url"`
	HTTPSURL    string    `json:"httpsURL"`
	TimeCreated time.Time `json:"timeCreated"`
	IsClaimed   bool      `json:"isClaimed"`
	IsPrivate   bool      `json:"isPrivate"`
	Filename    string    `json:"filename"`
	Format      string    `json:"format"`
	Size        int64     `json:"size"`
	Height      int       `json:"height"`
	Width       int       `json:"width"`
}

type Error struct {
	HTTPStatusCode int    `json:"httpStatusCode"`
	Cause          string `json:"cause"`
	Message        string `json:"message"`
}

func (e *Error) Error() string {
	return "ospry: " + e.Message
}

type RenderOpts struct {
	Format      string
	MaxHeight   int
	MaxWidth    int
	TimeExpired time.Time
}

// SetKey changes the api key used by the default client.
func SetKey(key string) {
	DefaultClient.Key = key
}

// UploadPublic calls UploadPublic on the default client.
func UploadPublic(filename string, data io.Reader) (*Metadata, error) {
	return DefaultClient.UploadPublic(filename, data)
}

// UploadPrivate calls UploadPrivate on the default client.
func UploadPrivate(filename string, data io.Reader) (*Metadata, error) {
	return DefaultClient.UploadPrivate(filename, data)
}

// Download calls Download on the default client.
func Download(url string, opts *RenderOpts) (io.ReadCloser, error) {
	return DefaultClient.Download(url, opts)
}

// Claim calls Claim on the default client.
func Claim(id string) (*Metadata, error) {
	return DefaultClient.Claim(id)
}

// GetMetadata calls GetMetadata on the default client.
func GetMetadata(id string) (*Metadata, error) {
	return DefaultClient.GetMetadata(id)
}

// MakePrivate calls MakePrivate on the default client.
func MakePrivate(id string) (*Metadata, error) {
	return DefaultClient.MakePrivate(id)
}

// MakePublic calls MakePublic on the default client.
func MakePublic(id string) (*Metadata, error) {
	return DefaultClient.MakePublic(id)
}

// Delete calls Delete on the default client.
func Delete(id string) error {
	return DefaultClient.Delete(id)
}

// FormatURL calls FormatURL on the default client.
func FormatURL(urlstr string, opts *RenderOpts) (string, error) {
	return DefaultClient.FormatURL(urlstr, opts)
}

// A Client performs authenticated API calls.
type Client struct {
	Key        string
	ServerURL  string
	HTTPClient *http.Client
}

// New creates a client that authenticates with the given key. By
// default, the client's HTTPClient is http.DefaultClient.
func New(key string) *Client {
	return &Client{
		Key:        key,
		ServerURL:  "https://api.ospry.io/v1",
		HTTPClient: http.DefaultClient,
	}
}

// UploadPublic uploads a public image with the given filename. The
// image will be automatically claimed if the client was initialized
// with your secret key.
func (c *Client) UploadPublic(filename string, data io.Reader) (*Metadata, error) {
	return c.uploadImage(filename, false, data)
}

// UploadPrivate uploads a private image with the given filename. The
// image will be automatically claimed if the client was initialized
// with your secret key.
func (c *Client) UploadPrivate(filename string, data io.Reader) (*Metadata, error) {
	return c.uploadImage(filename, true, data)
}

func (c *Client) uploadImage(filename string, isPrivate bool, data io.Reader) (*Metadata, error) {
	u, err := url.Parse(c.ServerURL)
	if err != nil {
		return nil, err
	}
	u.Path += "/images"
	q := url.Values{}
	q.Add("filename", filename)
	q.Add("isPrivate", strconv.FormatBool(isPrivate))
	u.RawQuery = q.Encode()
	// Content-type doesn't need to match the image but it needs to be
	// something that indicates image data (rather than
	// multipart/form-data).
	res, err := c.curl("POST", u.String(), "image/jpeg", data)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	return parseMetadata(res.Body)
}

// GetMetadata retrieves the metadata for the image with the given id.
func (c *Client) GetMetadata(id string) (*Metadata, error) {
	u, err := url.Parse(c.ServerURL)
	if err != nil {
		return nil, err
	}
	u.Path += "/images/" + id
	res, err := c.curl("GET", u.String(), "application/json", nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	return parseMetadata(res.Body)
}

// Download retrieves the image data at the given url. You can render
// a modified image by providing a non-nil RenderOpts.
func (c *Client) Download(urlstr string, opts *RenderOpts) (io.ReadCloser, error) {
	var err error
	urlstr, err = FormatURL(urlstr, opts)
	if err != nil {
		return nil, err
	}
	res, err := c.HTTPClient.Get(urlstr)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != 200 {
		return nil, errors.New("ospry: download resulted in non-200 status")
	}
	return res.Body, nil
}

// Claim claims ownership of an image that was uploaded
// client-side. You need to claim images to prevent them from
// disappearing (if you've turned claiming on in your account
// settings).
func (c *Client) Claim(id string) (*Metadata, error) {
	return c.patch(id, map[string]interface{}{
		"isClaimed": true,
	})
}

// MakePrivate makes an image an private if it isn't already. Private
// images can be downloaded by anyone who has an unexpired, signed url
// to that image (see FormatURL).
func (c *Client) MakePrivate(id string) (*Metadata, error) {
	return c.patch(id, map[string]interface{}{
		"isPrivate": true,
	})
}

// MakePublic makes an image public if it isn't already. Public images
// can be downloaded by anyone who has the url to that image.
func (c *Client) MakePublic(id string) (*Metadata, error) {
	return c.patch(id, map[string]interface{}{
		"isPrivate": false,
	})
}

// Delete deletes an image. Attempts to retrieve images that have been
// deleted will result in 404s.
func (c *Client) Delete(id string) error {
	u, err := url.Parse(c.ServerURL)
	if err != nil {
		return err
	}
	u.Path += "/images/" + id
	res, err := c.curl("DELETE", u.String(), "application/json", nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	_, err = parseMetadata(res.Body)
	return err
}

// FormatURL modifies an image url to produce a url that can be used
// to download a modified image (e.g. resized). If TimeExpired is
// given, the url is signed with the client's key and can be used to
// download access a private image until TimeExpired has past. An
// error is returned if the given url is invalid.
func (c *Client) FormatURL(urlstr string, opts *RenderOpts) (string, error) {
	if opts == nil {
		opts = &RenderOpts{}
	} else {
		opts = &RenderOpts{
			Format:      opts.Format,
			MaxHeight:   opts.MaxHeight,
			MaxWidth:    opts.MaxWidth,
			TimeExpired: opts.TimeExpired,
		}
	}
	u, err := url.Parse(urlstr)
	if err != nil {
		return "", err
	}
	q := u.Query()
	if opts.Format == "" && q.Get("format") != "" {
		opts.Format = q.Get("format")
	}
	if opts.MaxWidth == 0 && q.Get("maxWidth") != "" {
		mw64, err := strconv.ParseInt(q.Get("maxWidth"), 10, 0)
		if err != nil {
			return "", err
		}
		opts.MaxWidth = int(mw64)
	}
	if opts.MaxHeight == 0 && q.Get("maxHeight") != "" {
		mh64, err := strconv.ParseInt(q.Get("maxHeight"), 10, 0)
		if err != nil {
			return "", err
		}
		opts.MaxHeight = int(mh64)
	}
	if opts.TimeExpired.IsZero() && q.Get("timeExpired") != "" {
		opts.TimeExpired, err = time.Parse(time.RFC3339Nano, q.Get("timeExpired"))
		if err != nil {
			return "", err
		}
	}
	var imgURL string
	if q.Get("url") != "" {
		imgURL = q.Get("url")
		u, err = url.Parse(imgURL)
		if err != nil {
			return "", err
		}
	} else {
		u.RawQuery = ""
		imgURL = u.String()
	}

	// Signed?
	if !opts.TimeExpired.IsZero() {
		timeExpired := opts.TimeExpired.Format(time.RFC3339Nano)
		payload := imgURL + "?timeExpired=" + url.QueryEscape(timeExpired)
		h := hmac.New(sha256.New, []byte(c.Key))
		h.Write([]byte(payload))
		q.Set("signature", base64.StdEncoding.EncodeToString(h.Sum(nil)))
		q.Set("url", imgURL)
		q.Set("timeExpired", timeExpired)
		u.Host = "api.ospry.io"
		u.Path = "/"
		u.Scheme = "https"
	}

	if opts.Format != "" {
		found := false
		for _, f := range Formats {
			if opts.Format == f {
				found = true
				break
			}
		}
		if !found {
			return "", errors.New("ospry: invalid format " + opts.Format)
		}
		q.Set("format", opts.Format)
	}
	if opts.MaxHeight < 0 {
		return "", errors.New("ospry: MaxHeight can't be negative")
	}
	if opts.MaxHeight > 0 {
		q.Set("maxHeight", strconv.FormatInt(int64(opts.MaxHeight), 10))
	}
	if opts.MaxWidth < 0 {
		return "", errors.New("ospry: MaxWidth can't be negative")
	}
	if opts.MaxWidth > 0 {
		q.Set("maxWidth", strconv.FormatInt(int64(opts.MaxWidth), 10))
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (c *Client) curl(method, urlstr string, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, urlstr, body)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.Key, "")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return c.HTTPClient.Do(req)
}

func (c *Client) patch(id string, p interface{}) (*Metadata, error) {
	u, err := url.Parse(c.ServerURL)
	if err != nil {
		return nil, err
	}
	u.Path += "/images/" + id
	b, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	res, err := c.curl("PUT", u.String(), "application/json", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	return parseMetadata(res.Body)
}

func parseMetadata(body io.Reader) (*Metadata, error) {
	var res struct {
		Metadata *Metadata `json:"metadata"`
		Error    *Error    `json:"error"`
	}
	if err := json.NewDecoder(body).Decode(&res); err != nil {
		return nil, err
	}
	if res.Error != nil {
		return nil, res.Error
	}
	return res.Metadata, nil
}
