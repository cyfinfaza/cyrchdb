package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc64"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

var crctable *crc64.Table = crc64.MakeTable(crc64.ISO)

type completionJob struct {
	Index	int
	Working	[1]byte
	Status	[3]byte
}
var completionJobs chan completionJob = make(chan completionJob)

type readJob struct {
	Index	int		`json:"index"`
	Url		string	`json:"url"`
}
var readJobs chan readJob = make(chan readJob)

var writeJobs chan []byte = make(chan []byte)

var exitRequest chan bool = make(chan bool)

var writeError chan bool = make(chan bool)

var cache []uint64
var cacheLock sync.Mutex

var indicies []int = []int{0}

type urlEntry struct{
	done	bool
	working	bool
	status	string
	url		string
}

func decodeEntry(data []byte) urlEntry{
	var entry urlEntry
	entry.done = data[0]=='1'
	entry.working = data[2]=='1'
	entry.status = string(data[4:7])
	entry.url = string(data[8:])
	return entry
}

var boolStrMap map[bool]string = map[bool]string{
	true:"1",
	false:"0",
}

func encodeEntry(entry urlEntry) []byte{
	outStr := fmt.Sprintf("%s\t%s\t%s\t%s\n",
		boolStrMap[entry.done],
		boolStrMap[entry.working],
		entry.status,
		entry.url,
	)
	return []byte(outStr)
}

func readEntryFromResults(file os.File) (urlEntry, int64, bool){
	readSize := 1000
	for{
		data := make([]byte, readSize)
		readIndex := int64(indicies[len(indicies)-1])
		// log.Println(readIndex)
		_, err := file.ReadAt(data, readIndex)
		end := bytes.Index(data, []byte("\n"))
		if end == -1 && (err != nil || readSize > 10000) {
			return urlEntry{}, readIndex, false
		}
		if end == -1 {
			readSize += 1000
			continue
		}
		indicies = append(indicies, end+int(readIndex)+1)
		return decodeEntry(data[:end]), readIndex, true
	}
}

func readSpawner(file os.File){
	for{
		entry, readIndex, success := readEntryFromResults(file)
		if !success {
			log.Printf("Read spawner cannot find any more entries @ %d\n", readIndex)
			time.Sleep(time.Millisecond*500)
			continue
		}
		if !entry.done{
			readJobs <- readJob{
				Index: int(readIndex),
				Url: entry.url,
			}
		}
	}
}

func resultsWriteHandler(file os.File){
	for{
		select{
		case job := <- completionJobs:
			if job.Index > 0{
				lastChar := make([]byte, 1)
				file.ReadAt(lastChar, int64(job.Index-1))
				if lastChar[0] != []byte("\n")[0] {
					log.Println("WARNING Invalid completion request placed. Ignoring request")
					writeError <- true
					continue
				}
			}
			file.WriteAt([]byte("1"), int64(job.Index))
			file.WriteAt(job.Working[:], int64(job.Index+2))
			file.WriteAt(job.Status[:], int64(job.Index+4))
			writeError <- false
		case job := <- writeJobs:
			filestats, _ := file.Stat()
			file.WriteAt(job, filestats.Size())
		}
	}
}

func lineCounter(r io.Reader) (int, error) {
    buf := make([]byte, 32*1024)
    count := 0
    lineSep := []byte{'\n'}

    for {
        c, err := r.Read(buf)
        count += bytes.Count(buf[:c], lineSep)

        switch {
        case err == io.EOF:
            return count, nil

        case err != nil:
            return count, err
        }
    }
}

func cacheInsert(urls [][]byte){
	cacheLock.Lock()
	defer cacheLock.Unlock()
	for _, url := range urls{
		hash := crc64.Checksum(url, crctable)
		index := sort.Search(len(cache), func(i int) bool { return cache[i]>=hash })
		cache = append(cache, 0)
		copy(cache[index+1:], cache[index:])
		cache[index] = hash
	}
}

func cacheSearch(url []byte) bool{
	cacheLock.Lock()
	defer cacheLock.Unlock()
	hash := crc64.Checksum(url, crctable)
	index := sort.Search(len(cache)-1, func(i int) bool { return cache[i]>=hash })
	return cache[index] == hash
}

func cachePopulate(file os.File, size int){
	if size%8 != 0{
		panic("Cannot populate cache with file size not a multiple of 8")
	}
	for i:=0; i<size; i+=8 {
		binaryData := make([]byte, 8)
		file.ReadAt(binaryData, int64(i))
		cache = append(cache, binary.LittleEndian.Uint64(binaryData))
	}
}

func cacheSave(file os.File){
	for index, hash := range cache{
		binaryData := make([]byte, 8)
		binary.LittleEndian.PutUint64(binaryData, hash)
		file.WriteAt(binaryData, int64(index*8))
	}
}

func httpHandler(w http.ResponseWriter, r *http.Request){
	w.Header().Add("server", "cyrchdb")
	switch r.Method {
	case "GET":
		switch r.URL.Path{
		case "/":
			fmt.Fprint(w, "Welcome to cyrchdb")
		case "/read":
			job := <- readJobs
			data, _ := json.Marshal(job)
			w.Write(data)
		default:
			fmt.Fprintf(w, "You are in uncharted territory. %s %s", r.Method, r.URL.Path)
		}
	case "POST":
		switch r.URL.Path{
		case "/complete":
			body, err1 := ioutil.ReadAll(r.Body)
			var job struct {
				Index	int		`json:"index"`
				Working	bool	`json:"working"`
				Status	string	`json:"status"`
			}
			err2 := json.Unmarshal(body, &job)
			if err1!=nil || err2!=nil{
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			var statusBytes [3]byte
			copy(statusBytes[:], []byte(job.Status))
			completionJobs <- completionJob{
				Index: job.Index,
				Working: map[bool][1]byte{
					true:	{byte('1')},
					false:	{byte('1')},
				}[job.Working],
				Status: statusBytes,
			}
			if <-writeError {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, "Invalid completion request. Request ignored")
			} else {
				fmt.Fprint(w, "OK")
			}
		case "/introduce":
			var urls []string;
			body, err1 := ioutil.ReadAll(r.Body)
			err2 := json.Unmarshal(body, &urls)
			if err1!=nil || err2!=nil{
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			var writeJobsToSend [][]byte
			var urlsToCache [][]byte
			for _, url := range urls{
				url = strings.ReplaceAll(url, "\n", "")
				url = strings.ReplaceAll(url, "\t", "")
				present := false
				for _, binaryUrl := range urlsToCache{
					if bytes.Compare(binaryUrl, []byte(url))==0{
						present = true
						break
					}
				}
				if !cacheSearch([]byte(url)) && !present{
					writeJobsToSend = append(writeJobsToSend, encodeEntry(urlEntry{
						url:		url,
						status:		"000",
						working: 	false,
						done:		false,
					}))
					urlsToCache = append(urlsToCache, []byte(url))
				}
			}
			cacheInsert(urlsToCache)
			for _, writeJob := range writeJobsToSend{
				writeJobs <- writeJob
			}
			fmt.Fprint(w, len(writeJobsToSend))
		default:
			fmt.Fprint(w, r.Body)
		}
	default:
		fmt.Fprint(w, "Unsupported method")
	}
}

func main(){
	log.Println("Starting")
	if len(os.Args)>1 {
		cmnd := os.Args[1]
		switch cmnd {
		case "cache-regen":
			log.Println("Started in cache-regen mode")
			log.Println("Hint: if this cache-regen does not work, delete the `cache.bin` file and try again")
			log.Println("Opening files")
			resultsFile, err := os.OpenFile("results.tsv", os.O_RDONLY, 0666)
			if err != nil {
				log.Println("Error opening results.tsv. Does it exist?")
				panic(err)
			}
			cacheFile, err := os.OpenFile("cache.bin", os.O_RDWR|os.O_CREATE, 0666)
			if err != nil {
				panic(err)
			}
			log.Println("Generating cache data")
			entry, _, success := readEntryFromResults(*resultsFile)
			for success {
				cacheInsert([][]byte{[]byte(entry.url)})
				entry, _, success = readEntryFromResults(*resultsFile)
			}
			log.Println("Saving cache data")
			cacheSave(*cacheFile)
			log.Println("Closing files")
			cacheFile.Close()
			resultsFile.Close()
			log.Println("Done")
			os.Exit(0)
		default:
			log.Fatalf("Started in unknown mode: `%s`\n", cmnd)
		}
	}
	cacheFile, err := os.OpenFile("cache.bin", os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		panic(err)
	}
	defer cacheFile.Close()
	resultsFile, err := os.OpenFile("results.tsv", os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		panic(err)
	}
	defer resultsFile.Close()
	numResults, _ := lineCounter(resultsFile)
	cacheStats, _ := cacheFile.Stat()
	cacheSize := cacheStats.Size()
	cacheAppearsInvalid := cacheSize%8!=0 || cacheSize/8!=int64(numResults)
	malformedString := "No"
	if cacheAppearsInvalid {
		malformedString = "Yes"
	}
	log.Printf("Current Results: %d  Cache size: %d  Cache item quantity: %d  Cache appears invalid: %s", numResults, cacheSize, cacheSize/8, malformedString)
	if cacheAppearsInvalid {
		log.Fatal("Cache appears invalid. Run `cyrchdb cache-regen` and try again. Aborting")
	}
	log.Print("Populating in-memory cache")
	cachePopulate(*cacheFile, int(cacheSize))
	s := http.Server{
		Addr: ":8080",
		Handler: http.HandlerFunc(httpHandler),
	}
	go readSpawner(*resultsFile)
	go resultsWriteHandler(*resultsFile)
	go s.ListenAndServe()
	log.Println("HTTP interface active on port 8080")
	log.Println("Press enter to safely shut down server")
	fmt.Scanln()
	log.Println("Saving cache")
	cacheSave(*cacheFile)
	log.Println("Done. Exiting")
// 	for {
// 		fmt.Print("cyrchdb> ")
// 		var in string;
// 		fmt.Scanln(&in)
// 		switch strings.Trim(in, " "){
// 		case "exit":
// 			log.Println("Exiting...")
// 			os.Exit(0);
// 		case "count":
// 			println("Not yet implemented.")
// 		case "help":
// 			println(
// `exit	stop server
// count	show current result count
// help	show help`)
// 		default:
// 			println("Unknown command. Type `help` for help.")
// 		}
// 	}
}