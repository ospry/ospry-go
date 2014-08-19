package main

import (
	"container/list"
	"encoding/json"
	"flag"
	"html/template"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	ospry "github.com/ospry/ospry-go"
	"github.com/rynlbrwn/route"
)

var publicKey string

func main() {
	var secretKey string
	flag.StringVar(&secretKey, "secretkey", "", "secret api key")
	flag.StringVar(&publicKey, "publickey", "", "public api key")
	flag.Parse()

	if secretKey == "" || publicKey == "" {
		log.Fatal("both -secretkey and -publickey are required")
	}

	ospry.SetKey(secretKey)

	route.Get("/", GetRoot)
	route.Get("/images", GetImages, "images")
	route.Pst("/images", PostImages)
	route.Pst("/make-private", PostMakePrivate)
	route.Pst("/make-public", PostMakePublic)
	route.Pst("/claim", PostClaim)
	log.Fatal(http.ListenAndServe(":8080", route.DefaultHandler))
}

func GetRoot(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, route.URL("images"), 301)
}

func GetImages(w http.ResponseWriter, r *http.Request) {
	t, ok := tmpl("index")
	if !ok {
		http.Error(w, "index template not found", 500)
		return
	}
	metadatas := getMetadatas()
	publicURLs := []string{}
	privateURLs := []string{}
	for _, metadata := range metadatas {
		privateURL, err := ospry.FormatURL(metadata.URL, &ospry.RenderOpts{
			TimeExpired: time.Now().Add(time.Minute),
		})
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		publicURLs = append(publicURLs, metadata.URL)
		privateURLs = append(privateURLs, privateURL)
	}
	m := map[string]interface{}{
		"PublicURLs":  publicURLs,
		"PrivateURLs": privateURLs,
		"PublicKey":   publicKey,
	}
	if err := t.Execute(w, m); err != nil {
		log.Println(err)
	}
}

func PostImages(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") &&
		r.FormValue("method") == "DELETE" {
		DeleteImages(w, r)
		return
	}
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	for {
		p, err := mr.NextPart()
		if err != nil {
			if err == io.EOF {
				break
			}
			http.Error(w, err.Error(), 400)
		}
		switch p.FormName() {
		case "file":
			m, err := ospry.UploadPrivate(p.FileName(), p)
			if err != nil {
				log.Println(err.Error())
				continue
			}
			saveMetadata(m)
		}
	}
	http.Redirect(w, r, route.URL("images"), 303)
}

func DeleteImages(w http.ResponseWriter, r *http.Request) {
	m := getMetadatas()
	for _, v := range m {
		if err := ospry.Delete(v.ID); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		deleteMetadata(v)
	}
	http.Redirect(w, r, route.URL("images"), 303)
}

func PostMakePrivate(w http.ResponseWriter, r *http.Request) {
	m := getMetadatas()
	for _, v := range m {
		if _, err := ospry.MakePrivate(v.ID); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	http.Redirect(w, r, route.URL("images"), 303)
}

func PostMakePublic(w http.ResponseWriter, r *http.Request) {
	m := getMetadatas()
	for _, v := range m {
		if _, err := ospry.MakePublic(v.ID); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	http.Redirect(w, r, route.URL("images"), 303)
}

func PostClaim(w http.ResponseWriter, r *http.Request) {
	m := &ospry.Metadata{}
	if err := json.NewDecoder(r.Body).Decode(m); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	m, err := ospry.Claim(m.ID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	saveMetadata(m)
	privateURL, err := ospry.FormatURL(m.URL, &ospry.RenderOpts{
		TimeExpired: time.Now().Add(time.Minute),
	})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	d := map[string]interface{}{
		"privateUrl": privateURL,
	}
	if err := json.NewEncoder(w).Encode(d); err != nil {
		log.Println(err)
	}
}

func tmpl(name string) (*template.Template, bool) {
	tmpls := template.Must(template.ParseGlob("*.html"))
	t := tmpls.Lookup(name)
	return t, (t != nil)
}

// Fake database.
var metadatas = list.New()
var lock sync.RWMutex

func saveMetadata(m *ospry.Metadata) {
	lock.Lock()
	defer lock.Unlock()
	metadatas.PushBack(m)
}

func deleteMetadata(m *ospry.Metadata) {
	lock.Lock()
	defer lock.Unlock()
	for e := metadatas.Front(); e != nil; e = e.Next() {
		if e.Value.(*ospry.Metadata).ID == m.ID {
			metadatas.Remove(e)
		}
	}
}

func getMetadatas() []*ospry.Metadata {
	lock.RLock()
	defer lock.RUnlock()
	m := []*ospry.Metadata{}
	for e := metadatas.Front(); e != nil; e = e.Next() {
		v := e.Value.(*ospry.Metadata)
		m = append(m, &ospry.Metadata{
			ID:          v.ID,
			URL:         v.URL,
			TimeCreated: v.TimeCreated,
			IsClaimed:   v.IsClaimed,
			IsPrivate:   v.IsPrivate,
			Filename:    v.Filename,
			Format:      v.Format,
			Size:        v.Size,
			Height:      v.Height,
			Width:       v.Width,
		})
	}
	return m
}
