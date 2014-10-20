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

const downloadDir string = "/nas/3TB/Media/In/rtve/d"
const cacheDir string = "/nas/3TB/Media/In/rtve/d/.cache"

func stripchars(str, chr string) string {
	return strings.Map(func(r rune) rune {
		if strings.IndexRune(chr, r) < 0 {
			return r
		}
		return -1
	}, str)
}

type Serie struct {
	ShortTitle string
	Id         int `json:",string"`
	VideosRef  string
}

type Series struct {
	Series []Serie
}

type Episode struct {
	ShortTitle  string
	LongTitle   string
	Episode     int
	Id          int `json:",string"`
	ProgramInfo struct {
		Title string
	}
	Private struct {
		URL    string
		Offset int
	}
	Qualities []EpisodeFile
}

type EpisodeFile struct {
	Type     string
	Preset   string
	Filesize int64
	Duration int
}
type Programas struct {
	Page struct {
		Items []Episode
	}
}

func makeCacheDir() {
	err := os.MkdirAll("/nas/3TB/Media/In/rtve/d/.cache", 0755)
	if err != nil {
		log.Fatal(err)
	}
}

func PKCS7Padding(data []byte) []byte {
	blockSize := 16
	padding := blockSize - len(data)%blockSize
	padtext := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(data, padtext...)

}

func UnPKCS7Padding(data []byte) []byte {
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
	iv := []byte("\000\000\000\000\000\000\000\000\000\000\000\000\000\000\000\000")

	encrypter := cipher.NewCBCEncrypter(ckey, iv)

	str = PKCS7Padding(str)
	out := make([]byte, len(str))

	encrypter.CryptBlocks(out, str)

	base64Out := base64.URLEncoding.EncodeToString(out)

	decrypter := cipher.NewCBCDecrypter(ckey, iv)
	base64In, _ := base64.URLEncoding.DecodeString(base64Out)
	in := make([]byte, len(base64In))
	decrypter.CryptBlocks(in, base64In)

	in = UnPKCS7Padding(in)
	return base64Out
}

func orfeo(id int, t int64) string {
	mobilekey := "k0rf30jfpmbn8s0rcl4nTvE0ip3doRan"
	secret := fmt.Sprintf("%d_es_%d", id, t)
	orfeo := cryptaes(secret, mobilekey)
	return "http://www.rtve.es/ztnr/consumer/orfeo/video/" + orfeo

}

func cacheFile(url string) string {
	file := fmt.Sprintf("%x", sha256.Sum256([]byte(url)))
	path := path.Join("/nas/3TB/Media/In/rtve/d/.cache", file)
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

func (e *Episode) remote(offset int) int {
	t := time.Now().Local().Add(time.Duration(offset) * time.Second)
	ts := t.UnixNano() / int64(time.Millisecond)
	videourl := orfeo(e.Id, ts)
	res, err := http.Head(videourl)
	if err != nil {
		log.Fatal(err)
	}
	if res.StatusCode == 200 {
		e.Private.URL = videourl
		e.Private.Offset = offset
	}
	return res.StatusCode
}

func (e *Episode) writeData() {
	b, err := json.Marshal(e)
	if err != nil {
		fmt.Println("error:", err)
	}
	filename := fmt.Sprintf("%d.json", e.Id)
	err = ioutil.WriteFile(path.Join("/nas/3TB/Media/In/rtve/d", filename), b, 0644)
	if err != nil {
		log.Fatal(err)
	}
}

func (e *Episode) stat() {

	for i := 89400; i < 90600; i = i + 15 {
		r := e.remote(i)
		if r == 200 {
			log.Println(">", e)
			return
		}
		r = e.remote(i - 90000)
		if r == 200 {
			log.Println(">", e)
			return
		}
		r = e.remote(i - 120000)
		if r == 200 {
			log.Println(">", e)
			return
		}
	}
	log.Println("x", e)
}

func (e *Episode) download() {
	filename := fmt.Sprintf("%d.mp4", e.Id)
	filename = path.Join(downloadDir, filename)

	fi, err := os.Stat(filename)
	if err != nil && !os.IsNotExist(err) {
		log.Fatal(err)
	}
	if !os.IsNotExist(err) && (fi.Size() == e.Qualities[0].Filesize || fi.Size() == e.Qualities[1].Filesize) {
		log.Println("> Sile", e)
		return
	}

	output, err := os.Create(filename)
	if err != nil {
		fmt.Println("Error while creating", filename, "-", err)
		return
	}
	defer output.Close()

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
}
func main() {
	makeCacheDir()
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
			e.writeData()
			e.download()
		}
	}
}
