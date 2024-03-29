package main

/*
www.rtve.es/api/clan/series/spanish/todas (follow redirect)

http://www.rtve.es/api/programas/80170/videos
*/
import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

func stripchars(str, chr string) string {
	return strings.Map(func(r rune) rune {
		if strings.IndexRune(chr, r) < 0 {
			return r
		}
		return -1
	}, str)
}

type Config struct {
	Dirs         map[string]string
	Keys         map[string]string
	Programs     []ProgramConfig
	Verbose      bool
	Nocache      bool
	ItemsPerPage int
}

type ProgramConfig struct {
	Id   int
	Name string
}

func (c *Config) load(f string) {
	content, err := ioutil.ReadFile(f)
	if err != nil {
		log.Fatal(err)
	}

	err = json.Unmarshal(content, c)
	if err != nil {
		log.Fatal(err)
	}
}

/*
Episode is a representation of each episode
*/
type Episode struct {
	ShortTitle       string
	LongTitle        string
	ShortDescription string
	LongDescription  string
	Episode          int
	ID               int `json:",string"`
	ProgramRef       string
	ProgramInfo      struct {
		Title string
	}
	Private struct {
		URL       string
		EndURL    string
		Offset    int
		Size      int64
		Ext       string
		Videofile string
	}
	Qualities []struct {
		Type     string
		Preset   string
		Filesize int64
		Duration int
	}
}

/*
Programa is a representation of the list of available episodes of a program
*/
type Programa struct {
	Name             string
	WebOficial       string
	WebRtve          string
	Description      string
	LongTitle        string
	ShortDescription string
	LongDescription  string
	ID               int `json:",string"`
	episodios        []Episode
}

type videosPrograma struct {
	Page struct {
		TotalPages  int
		Total       int
		NumElements int
		Number      int
		Offset      int
		Size        int
		Items       []Episode
	}
}
type Programas struct {
	Page struct {
		TotalPages int
		Items      []Programa
	}
}

func makeDirs() {
	for _, dir := range config.Dirs {
		err := os.MkdirAll(dir, 0755)
		if err != nil {
			log.Fatal(err)
		}
	}
}

func pkcsS7Padding(data []byte) []byte {
	blockSize := 16
	padding := blockSize - len(data)%blockSize
	padtext := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(data, padtext...)
}

func unpkcs7Padding(data []byte) []byte {
	length := len(data)
	unpadding := int(data[length-1])
	return data[:(length - unpadding)]
}

func getTime() int64 {
	return time.Now().Add(150*time.Hour).Round(time.Hour).UnixNano() / int64(time.Millisecond)
}

func cryptaes(text, key string) string {

	ckey, err := aes.NewCipher([]byte(key))
	if nil != err {
		log.Fatal(err)
	}

	str := []byte(text)
	var a [16]byte
	iv := a[:]

	encrypter := cipher.NewCBCEncrypter(ckey, iv)

	str = pkcsS7Padding(str)
	out := make([]byte, len(str))

	encrypter.CryptBlocks(out, str)

	base64Out := base64.StdEncoding.EncodeToString(out)
	return base64Out
}

func ztnrurl(id int, t int64, clase string) string {
	baseurl := fmt.Sprintf("http://www.rtve.es/ztnr/consumer/%s/video", clase)

	secret := fmt.Sprintf("%d_es_%d", id, t)
	url := fmt.Sprintf("%s/%s", baseurl, cryptaes(secret, config.Keys[clase]))
	return url
}

func cacheFile(url string) string {
	file := fmt.Sprintf("%x", sha256.Sum256([]byte(url)))
	path := path.Join(config.Dirs["cache"], file)
	return path
}

func read(url string, v interface{}) error {
	cache := cacheFile(url)
	fi, err := os.Stat(cache)
	if err != nil && !os.IsNotExist(err) {
		log.Fatal(err)
	}
	if config.Nocache || os.IsNotExist(err) || time.Now().Unix()-fi.ModTime().Unix() > 3*3600 {
		log.Println("Fetching", url, "to cache", cache)
		// Cache for 12h
		res, err := http.Get(url)
		if err != nil {
			log.Fatalf("read http. Get error: %v", err)
		}
		if res.StatusCode >= 400 {
			return fmt.Errorf("http. Unexpected HTTP Status Code %s downloading %s", res.StatusCode, url)
		}

		log.Printf("Reading %v", res.Request)

		content, err := ioutil.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			log.Fatal(err)
		}
		err = ioutil.WriteFile(cache, content, 0644)
		if err != nil {
			log.Fatal(err)
		}
	}

	content, err := ioutil.ReadFile(cache)
	if err != nil {
		log.Fatal(err)
	}

	//	log.Println(string(content[:]))

	err = json.Unmarshal(content, v)
	if err != nil {
		return fmt.Errorf("Got error %v unmarshaling %v", err, content)
	}
	return nil
}

func (p *Programa) getVideos(programid, page int) error {
	offset := (page - 1) * config.ItemsPerPage
	url := fmt.Sprintf("http://www.rtve.es/api/programas/%d/videos?size=%d&offset=%d&page=%d", programid, config.ItemsPerPage, offset, page)
	var videos videosPrograma

	err := read(url, &videos)
	if err != nil {
		log.Println(err)
		return fmt.Errorf("Error downloading page %d of %d", page, programid)
	}

	p.episodios = append(p.episodios, videos.Page.Items...)
	if len(videos.Page.Items) == 0 {
		return fmt.Errorf("Length 0 in videos.Page.Items for programid %d", programid)
	}

	log.Printf("Tenemos %d episodios de %s", videos.Page.NumElements, videos.Page.Items[0].ProgramInfo.Title)
	if videos.Page.Number < videos.Page.TotalPages {
		err = p.getVideos(programid, page+1)
		if err != nil {
			return err
		}
	}
	return nil
}

func (e *Episode) remote(class string) int {
	t := time.Now().UTC().Round(time.Second)
	ts := t.UnixNano() / int64(time.Millisecond)
	var videourl string
	videourl = ztnrurl(e.ID, ts, class)

	res, err := http.Head(videourl)
	if err != nil {
		log.Fatal(err)
	}
	if res.StatusCode == 200 {

		ui := strings.Index(res.Request.URL.String(), "/playlist.m3u8")
		e.Private.EndURL = res.Request.URL.String()[0:ui]

		ei := strings.LastIndex(e.Private.EndURL, ".")
		e.Private.Ext = e.Private.EndURL[ei:]

		/* FIXME: Hack to ignore m3u8 playlists
		rtve has changed the endurl to be hls playlists. It would be ideal
		to download the hls fragments in parallel and then create a single file.
		*/

		if e.Private.Ext == "" {
			e.Private.Ext = ".mp4"
			log.Println("WARNING: Empty extension. Forcing mp4. %v", e)
		}
		e.Private.Videofile = fmt.Sprintf("%d%s", e.ID, e.Private.Ext)
		e.Private.Size = res.ContentLength
		// FIXME: This is not the value we want to store

		e.Private.URL = videourl
	}
	return res.StatusCode
}
func (e *Episode) json() string {
	b, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		log.Println("json marshall error:", err)
	}
	return string(b[:])
}
func (e *Episode) writeData() {
	filename := fmt.Sprintf("%d.json", e.ID)
	err := ioutil.WriteFile(path.Join(config.Dirs["download"], filename), []byte(e.json()), 0644)
	if err != nil {
		log.Fatal(err)
	}
}

func debug(wat ...interface{}) {
	if config.Verbose {
		fmt.Fprintln(os.Stderr, wat)
	}
}
func (e *Episode) stat() bool {
	keyorder := []string{"oceano", "carites", "orfeo", "caliope"}

	gotcha := false
	for _, k := range keyorder {
		statusCode := e.remote(k)
		debug("e.stat()", e.ID, e.humanName(), "statusCode", statusCode)
		if statusCode == 200 {
			gotcha = true
			break
		}
	}
	if !gotcha {
		log.Println("No candidates for", e)
	}
	return gotcha
}

func (e *Episode) download() {
	if e.Private.Videofile == "" {
		log.Fatal("e.Private.Videofile is empty when trying to download")
	}
	filename := path.Join(config.Dirs["download"], e.Private.Videofile)

	fi, err := os.Stat(filename)
	if err != nil && !os.IsNotExist(err) {
		log.Fatal(err)
	}

	sizes := map[int64]bool{}
	if !os.IsNotExist(err) {
		if e.Qualities != nil {
			for _, q := range e.Qualities {
				sizes[q.Filesize] = true
			}
		}
		debug("sizes", sizes, len(sizes), "sizes[fi.Size()]=", sizes[fi.Size()], "sizes[fi.Size()+1]=", sizes[fi.Size()+1])
		if fi.Size() >= e.Private.Size && sizes[fi.Size()] {
			// Our file is bigger and canonical
			// fmt.Fprintln(os.Stdout, err, "> Sile", fi.Size(), e.Private.Size)
			return
		}

		if fi.Size() < e.Private.Size {
			if sizes[e.Private.Size] {
				log.Println("Better version of", e.ID, fi.Size(), "available. Remote size:", e.Private.Size)

			} else {
				// There's a greater size available but it's not listed. Better mak a backup of the local file.
				log.Println("Larger NOT CANONICAL version of", e.ID, fi.Size(), "available. Remote size:", e.Private.Size)
				log.Println("Backing up", filename, "to", filename+".bak")
				err = os.Rename(filename, filename+".bak")
				if err != nil {
					log.Println("Error moving", filename, "to", filename+".bak", err)
					return
				}
			}
		}
	}

	output, err := os.Create(filename + ".temp")
	if err != nil {
		log.Println("Error while creating", filename, "-", err)
		return
	}
	defer output.Close()
	log.Printf("Downloading %s (%d MB) from %s (%s)", e.Private.Videofile, e.Private.Size/1024/1024, e.Private.URL, e.Private.EndURL)

	response, err := http.Get(e.Private.EndURL)
	if err != nil {
		log.Println("Error while downloading", e.Private.EndURL, "-", err)
		return
	}
	defer response.Body.Close()

	n, err := io.Copy(output, response.Body)
	if err != nil {
		log.Println("Error while downloading", e.Private.EndURL, "-", err)
		return
	}
	err = os.Rename(filename+".temp", filename)
	if err != nil {
		log.Println("Error renaming", filename+".temp", "to", filename, err)
		return
	}
	log.Println(filename, "downloaded.", n, "bytes.")

}
func setupLog() *os.File {
	t, _ := time.Now().UTC().Truncate(time.Hour).MarshalText()
	ts := string(t[:])

	filename := fmt.Sprintf("%s.log", ts)
	logfile := path.Join(config.Dirs["log"], filename)
	log.Println("Logfile:", logfile, config.Dirs["log"])
	f, err := os.OpenFile(logfile, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("error opening file: %v", err)
	}

	log.SetFlags(log.LstdFlags)
	log.SetOutput(io.MultiWriter(f, os.Stdout))
	return f

}
func (e *Episode) fromURL(url string) {
	type RemoteEpisode struct {
		Page struct {
			Items []Episode
		}
	}
	var v RemoteEpisode
	read(url, &v)
	// log.Println(v)
	*e = v.Page.Items[0]
}
func (e *Episode) fromFile(f string) {
	content, err := ioutil.ReadFile(f)
	if err != nil {
		log.Fatal(err)
	}

	err = json.Unmarshal(content, e)
	if err != nil {
		log.Fatal(err)
	}
}
func (e *Episode) humanName() string {
	return fmt.Sprintf("%s %d - %s", e.ProgramInfo.Title, e.Episode, e.LongTitle)
}

func publish() {
	dirfiles, err := ioutil.ReadDir(config.Dirs["download"])
	if err != nil {
		log.Fatalf("error reading dir: %v", err)
	}
	for _, file := range dirfiles {
		if path.Ext(file.Name()) == ".json" {
			var e Episode
			e.fromFile(path.Join(config.Dirs["download"], file.Name()))
			if e.ProgramInfo.Title == "Turno de oficio" {
				continue
			}
			dir := path.Join(config.Dirs["publish"], e.ProgramInfo.Title)
			err := os.MkdirAll(dir, 0755)
			if err != nil {
				log.Fatal(err)
			}

			videofile := path.Join(config.Dirs["download"], e.Private.Videofile)

			filename := fmt.Sprintf("%s%s", e.humanName(), e.Private.Ext)
			publishFile := path.Join(dir, filename)
			// fmt.Println(e.ID, publishFile)
			// Episode debería tener las funciones de comprobar integridad
			err = os.Link(videofile, publishFile)
			if err != nil {
				if !os.IsExist(err) {
					log.Printf("Cannot publish %s (%d) to %s", videofile, e.ID, publishFile)
				}
			} else {
				log.Printf("Published %s to %s", videofile, publishFile)
			}

		}
	}
}

func indexFiles() {
	log.Println("Believe it or not I'm reindexing")
	dirfiles, err := ioutil.ReadDir(config.Dirs["download"])
	if err != nil {
		log.Fatalf("error reading dir: %v", err)
	}

	for _, file := range dirfiles {
		if path.Ext(file.Name()) == ".json" {
			var e Episode
			e.fromFile(path.Join(config.Dirs["download"], file.Name()))
			// fmt.Println(file.Name(), e.ID, e.Private.Size)
			// Episode debería tener las funciones de comprobar integridad
		}
	}
}

func remoteEpisode(id int) {
	var e Episode
	e.ID = id
	log.Println("Getting remoteEpisode", e.json())

	e.fromURL(fmt.Sprintf("http://www.rtve.es/api/videos/%d", id))
	log.Println("Stat of remoteEpisode", e.json())
	if e.stat() {
		log.Println("remoteEpisode", e.json())
		e.writeData() // should check if previous steps didn't work
		e.download()
	}

}

func listPrograms(page int) {
	type RemotePrograms struct {
		Page struct {
			Items      []Programa
			Number     int
			TotalPages int
		}
	}
	var rp RemotePrograms
	// var drp RemotePrograms
	url := fmt.Sprintf("http://www.rtve.es/api/agr-programas/490/programas.json?size=%d&page=%d", config.ItemsPerPage, page)
	err := read(url, &rp)
	if err != nil {
		log.Fatal(err)
	}
	if rp.Page.Number < rp.Page.TotalPages {
		listPrograms(rp.Page.Number + 1)
	}
	for _, v := range rp.Page.Items {
		fmt.Printf("{ \"id\": %d, \"name\": \"%s\" },\n", v.ID, v.Name)
		if config.Verbose {
			fmt.Printf("// %s: %s\n", v.Name, v.WebRtve)
		}
	}
}

var config Config

func main() {
	config.load(os.ExpandEnv("${HOME}/.local/rtve-alacarta.json"))
	setupLog()
	showconfig := false
	doindex := false
	dolist := false
	doepisode := 0
	config.ItemsPerPage = 50
	flag.BoolVar(&showconfig, "sc", false, "show config")
	flag.BoolVar(&config.Nocache, "nc", false, "nocache")
	flag.BoolVar(&config.Verbose, "v", false, "verbose")
	flag.BoolVar(&doindex, "i", false, "reindex the whole thing")
	flag.BoolVar(&dolist, "l", false, "list programs")
	flag.IntVar(&doepisode, "e", 0, "single episode")
	flag.Parse()
	debug("verbose active")
	if dolist {
		listPrograms(1)
		return
	}
	if showconfig {
		fmt.Println(config)
		return
	}
	if doindex {
		indexFiles()
		publish()
		return
	}
	if doepisode > 0 {
		remoteEpisode(doepisode)
		return
	}
	makeDirs()

	log.Printf("Starting %s (PID: %d) at %s", os.Args[0], os.Getpid, time.Now().UTC())

	for _, v := range config.Programs {
		var p Programa
		err := p.getVideos(v.Id, 1)
		if err != nil {
			log.Println(err)
			continue
		}
		for _, e := range p.episodios {
			if e.stat() {
				e.writeData() // should check if previous steps didn't work
				e.download()
			} else {
				log.Println("Cannot stat", e.humanName())
			}
		}
	}
	log.Printf("Finishing %s (PID: %d) at %s", os.Args[0], os.Getpid, time.Now().UTC())
}
