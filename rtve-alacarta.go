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

var dirs = map[string]string{
	"base":     "/nas/3TB/Media/In/rtve/",
	"download": "/nas/3TB/Media/In/rtve/d",
	"cache":    "/nas/3TB/Media/In/rtve/cache",
	"log":      "/nas/3TB/Media/In/rtve/log",
	"publish":  "/nas/3TB/Media/Video/Infantil",
}

func stripchars(str, chr string) string {
	return strings.Map(func(r rune) rune {
		if strings.IndexRune(chr, r) < 0 {
			return r
		}
		return -1
	}, str)
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
	ProgramInfo      struct {
		Title string
	}
	Private struct {
		URL    string
		EndURL string
		Offset int
		Size   int64
	}
	Qualities []struct {
		Type     string
		Preset   string
		Filesize int64
		Duration int
	}
	ProgramRef string
}

/*
Programas is a representation of the list of programs offered by the API
*/
type Programas struct {
	Page struct {
		Items []Episode
	}
}

func makeDirs() {
	for _, dir := range dirs {
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

func orfeo(id int, t int64) string {
	mobilekey := "k0rf30jfpmbn8s0rcl4nTvE0ip3doRan"
	secret := fmt.Sprintf("%d_es_%d", id, t)
	orfeo := cryptaes(secret, mobilekey)
	return "http://www.rtve.es/ztnr/consumer/orfeo/video/" + orfeo
}

func oceano(id int, t int64) string {
	tabletkey := "pmku579tg465GDjf1287gDFFED56788C"
	secret := fmt.Sprintf("%d_es_%d", id, t)
	oceano := cryptaes(secret, tabletkey)
	return "http://www.rtve.es/ztnr/consumer/oceano/video/" + oceano
}

func cacheFile(url string) string {
	file := fmt.Sprintf("%x", sha256.Sum256([]byte(url)))
	path := path.Join(dirs["cache"], file)
	return path
}

func read(url string, v interface{}) error {
	cache := cacheFile(url)
	fi, err := os.Stat(cache)
	if err != nil && !os.IsNotExist(err) {
		log.Fatal(err)
	}

	if os.IsNotExist(err) || time.Now().Unix()-fi.ModTime().Unix() > 12*3600 {
		log.Println("seguimos")
		// Cache for 12h
		res, err := http.Get(url)
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

	log.Println(string(content[:]))
	err = json.Unmarshal(content, v)
	if err != nil {
		log.Fatal(err)
	}
	return nil
}

func (e *Programas) get(programid int) {
	url := fmt.Sprintf("http://www.rtve.es/api/programas/%d/videos?size=60", programid)
	err := read(url, e)
	if err != nil {
		log.Fatal(err)
	}
	log.Println("Tenemos episodios de", e.Page.Items[0].ProgramInfo.Title)
}

func (e *Episode) remote(offset int, doOceano bool) int {
	t := time.Now().UTC().Round(time.Second).Add(time.Duration(offset) * time.Second)
	ts := t.UnixNano() / int64(time.Millisecond)
	var videourl string
	if doOceano {
		videourl = oceano(e.ID, ts)
	} else {
		videourl = orfeo(e.ID, ts)
	}

	res, err := http.Head(videourl)
	if err != nil {
		log.Fatal(err)
	}
	if res.StatusCode == 200 {
		e.Private.Size = res.ContentLength
		e.Private.EndURL = res.Request.URL.String()
		e.Private.URL = videourl
		e.Private.Offset = offset
	}
	return res.StatusCode
}
func (e *Episode) json() string {
	b, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		fmt.Println("error:", err)
	}
	return string(b[:])
}
func (e *Episode) writeData() {
	filename := fmt.Sprintf("%d.json", e.ID)
	err := ioutil.WriteFile(path.Join(dirs["download"], filename), []byte(e.json()), 0644)
	if err != nil {
		log.Fatal(err)
	}
}

func (e *Episode) stat() {
	if !e.statOceano(true) {
		e.statOceano(false)
	}
}
func (e *Episode) statOceano(doOceano bool) bool {

	for i := 0; i < 1000; i = i + 20 {

		r := e.remote(i, doOceano)
		if r == 200 {
			return true
		}
		r = e.remote(i+3600, doOceano) // UTC+1
		if r == 200 {
			log.Println(">", e)
			return true
		}

		r = e.remote(i+7200, doOceano) // UTC+2
		if r == 200 {
			log.Println(i, ">", e)
			return true
		}

		r = e.remote(i+60000, doOceano) // Fuzzing val
		if r == 200 {
			log.Println(">", e)
			return true
		}
		r = e.remote(i+30000, doOceano) // Fuzz
		if r == 200 {
			log.Println(">", e)
			return true
		}
		r = e.remote(i+90000, doOceano) // Fuzz
		if r == 200 {
			log.Println(">", e)
			return true
		}
	}
	log.Println("x", e)
	return false
}

func (e *Episode) download() {
	filename := fmt.Sprintf("%d.mp4", e.ID)
	filename = path.Join(dirs["download"], filename)

	fi, err := os.Stat(filename)
	if err != nil && !os.IsNotExist(err) {
		log.Fatal(err)
	}

	if !os.IsNotExist(err) {
		if fi.Size() >= e.Private.Size && e.Qualities != nil && (fi.Size() == e.Qualities[0].Filesize || fi.Size() == e.Qualities[1].Filesize) {
			// Our file is bigger and canonical
			// fmt.Fprintln(os.Stdout, err, "> Sile", fi.Size(), e.Private.Size)
			return
		}

		if fi.Size() < e.Private.Size {
			if e.Qualities != nil && (e.Private.Size == e.Qualities[0].Filesize || e.Private.Size == e.Qualities[1].Filesize) {
				log.Println("Better version of", e.ID, fi.Size(), "available. Remote size:", e.Private.Size)

			} else {
				// There's a greater size available but it's not listed. Better mak a backup of the local file.
				log.Println("Larger NOT CANONICAL version of", e.ID, fi.Size(), "available. Remote size:", e.Private.Size)
				log.Println("Backing up", filename, "to", filename+".bak")
				err = os.Rename(filename, filename+".bak")
				if err != nil {
					fmt.Println("Error moving", filename, "to", filename+".bak", err)
					return
				}
			}
		}
	}

	output, err := os.Create(filename + ".temp")
	if err != nil {
		fmt.Println("Error while creating", filename, "-", err)
		return
	}
	defer output.Close()
	log.Println("Downloading", e.ID, e.Private.URL, e.Private.EndURL)

	response, err := http.Get(e.Private.URL)
	if err != nil {
		fmt.Println("Error while downloading", e.Private.URL, "-", err)
		return
	}
	defer response.Body.Close()

	n, err := io.Copy(output, response.Body)
	if err != nil {
		fmt.Println("Error while downloading", e.Private.URL, "-", err)
		return
	}
	fmt.Println(n, "bytes downloaded.")
	err = os.Rename(filename+".temp", filename)
	if err != nil {
		fmt.Println("Error moving", filename+".temp", "to", filename, err)
		return
	}

}
func setupLog() *os.File {
	t, _ := time.Now().Truncate(time.Hour).MarshalText()
	ts := string(t[:])

	filename := fmt.Sprintf("%s.log", ts)
	logfile := path.Join(dirs["log"], filename)
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
	log.Println(v)
	//*e = v.Page.Items[0]
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
	dirfiles, err := ioutil.ReadDir(dirs["download"])
	if err != nil {
		log.Fatalf("error reading dir: %v", err)
	}
	for _, file := range dirfiles {
		if path.Ext(file.Name()) == ".json" {
			var e Episode
			e.fromFile(path.Join(dirs["download"], file.Name()))
			dir := path.Join(dirs["publish"], e.ProgramInfo.Title)
			err := os.MkdirAll(dir, 0755)
			if err != nil {
				log.Fatal(err)
			}
			filename := fmt.Sprintf("%s.mp4", e.humanName())
			publishFile := path.Join(dir, filename)
			fmt.Println(e.ID, publishFile)
			// Episode debería tener las funciones de comprobar integridad
		}
	}
}
func indexFiles() {
	log.Println("Believe it or not I'm reindexing")
	dirfiles, err := ioutil.ReadDir(dirs["download"])
	if err != nil {
		log.Fatalf("error reading dir: %v", err)
	}

	for _, file := range dirfiles {
		if path.Ext(file.Name()) == ".json" {
			var e Episode
			e.fromFile(path.Join(dirs["download"], file.Name()))
			fmt.Println(file.Name(), e.ID, e.Private.Size)
			// Episode debería tener las funciones de comprobar integridad
		}
	}
}

func test() {
	// 2808202
	var e Episode
	e.fromFile(path.Join(dirs["download"], "2808202.json"))
	e.stat()
	e.writeData()
	fmt.Println("Testing", e.json())
	e.download()

}
func remoteEpisode(i int) {
	var e Episode
	e.ID = i
	e.fromURL(fmt.Sprintf("http://www.rtve.es/api/videos/%d", i))
	log.Println("remoteEpisode", e)
	e.stat()
}

func main() {
	setupLog()
	dotest := false
	doindex := false
	doepisode := 0
	flag.BoolVar(&doindex, "i", false, "reindex the whole thing")
	flag.BoolVar(&dotest, "t", false, "test algorithms")
	flag.IntVar(&doepisode, "e", 0, "single episode")
	flag.Parse()
	if dotest {
		test()
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

	log.Println("marchando")
	programids := []int{
		80170, // Pokemon XY
		44450, // Pokemon Advanced Challenge
		41651, // Pokemon Advanced
		49230, // Pokemon Black White
		68590, // Pokemon Black White Teselia
		50650, // Desafío Champions Sendokai
	}
	for _, v := range programids {
		var p Programas
		p.get(v)
		for _, e := range p.Page.Items {
			e.stat()
			e.writeData() // should check if previous steps didn't work
			e.download()
		}
	}
}
