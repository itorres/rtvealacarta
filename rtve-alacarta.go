package main

/*
www.rtve.es/api/clan/series/spanish/todas (follow redirect)

http://www.rtve.es/api/programas/80170/videos
*/
import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"
	"time"
)

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
	Id          int `json:",string"`
	ProgramInfo struct {
		Title string
	}
}
type Programas struct {
  Page struct{
	  Items []Episode
  }
}

func makeCacheDir() {
	err := os.MkdirAll("/tmp/rtvealasaca", 0755)
	if err != nil {
		log.Fatal(err)
	}
}

func cacheFile(url string) string {
	file := fmt.Sprintf("%x", sha256.Sum256([]byte(url)))
	path := path.Join("/tmp/rtvealasaca", file)
	return path
}

func read(url string, v interface{}) error {
	cache := cacheFile(url)
	fi, err := os.Stat(cache)
	if err != nil && !os.IsNotExist(err) {
		log.Fatal(err)
	}

	if os.IsNotExist(err) || time.Now().Unix() - fi.ModTime().Unix() > 12*3600 {
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
  url := fmt.Sprintf("http://www.rtve.es/api/programas/%d/videos", programid)
	err := read(url, e)
	if err != nil {
		log.Fatal(err)
	}
  log.Println("Tenemos episodes")

}

func main() {
	makeCacheDir()
	log.Println("marchando")
	var pokemonxy Programas
	pokemonxy.get(80170)
  log.Printf("%+v", pokemonxy)
}
