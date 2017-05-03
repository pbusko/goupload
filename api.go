package main

import (
	"fmt"
	"github.com/spf13/viper"
	"io"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

var BaseDir string
var BaseURL *url.URL
var IndexPage string
var allowedMimeTypes []string
var listener net.Listener
var socketType string
var socket string
var uploadUrl string
var statusUrl string
var pfile string

type errorType struct {
	Value string `json:"error"`
}

func (m errorType) Error() string {
	return m.Value
}

type UploadedFile struct {
	Name string `json:"name"`
	Url  string `json:"url"`
}

type ApiResponse struct {
	Images []UploadedFile `json:"images"`
}

func (response *ApiResponse) AddFile(file UploadedFile) {
	response.Images = append(response.Images, file)
}

func init() {
	viper.SetConfigName("api")
	viper.AddConfigPath(filepath.Base(os.Args[1]))
	err := viper.ReadInConfig()
	if err != nil {
		log.Fatal(err)
	}

	BaseDir = viper.GetString("upload.path")
	BaseURL, _ = url.Parse(viper.GetString("http.base_url"))
	IndexPage = viper.GetString("http.index_page")
	allowedMimeTypes = strings.Split(viper.GetString("upload.mime_types"), ";")
	uploadUrl = viper.GetString("http.upload_url")
	statusUrl = viper.GetString("http.status_url")
	pfile = viper.GetString("base.pidfile")

	socketType = viper.GetString("base.socket_type")
	if socketType == "tcp" {
		socket = viper.GetString("base.tcp_socket")
		listener, err = net.Listen("tcp", socket)
		if err != nil {
			log.Fatal(err)
		}
	} else if socketType == "unix" {
		socket = viper.GetString("base.unix_socket")
		if _, err = os.Stat(socket); err == nil {
			err = os.Remove(socket)
			if err != nil {
				log.Fatal(err)
			}
		}

		listener, err = net.Listen("unix", socket)
		if err != nil {
			log.Fatal(err)
		}
	} else {
		log.Fatal("Unknown socket type, check your config.")
	}
}

func savePidFile(pid int) error {
	data := []byte(strconv.Itoa(pid))
	f, err := os.OpenFile(pfile, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(data)
	if err != nil {
		return err
	}
	return err
}

func checkMime(m *string) bool {
	for _, mi := range allowedMimeTypes {
		if *m == mi {
			return true
		}
	}
	return false
}

func validateMimeType(f []*multipart.FileHeader) (err error) {
	mime_buffer := make([]byte, 512)

	for i, _ := range f {
		file, err := f[i].Open()
		defer file.Close()

		_, err = file.Read(mime_buffer)
		if err != nil {
			return err
		}
		file.Seek(0, 0)
		contentType := http.DetectContentType(mime_buffer)
		if !checkMime(&contentType) {
			err = &errorType{"One or more files with forbidden MIME-type received. Aborting"}
			return err
		}

	}
	err = nil
	return err
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "{\"status\": \"running\"}")
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {

	switch r.Method {

	case "GET":
		fmt.Fprintf(w, IndexPage)

	case "POST":
		//response := ApiResponse{}

		err := r.ParseMultipartForm(200000)
		if err != nil {
			fmt.Fprintln(w, err)
			return
		}

		files := r.MultipartForm.File["file"]
		err = validateMimeType(files)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		for i, _ := range files {
			file, err := files[i].Open()
			defer file.Close()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			out, err := os.Create(BaseDir + files[i].Filename)
			defer out.Close()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			_, err = io.Copy(out, file)

			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			fmt.Fprintf(w, "Files uploaded successfully : ")
			fmt.Fprintf(w, files[i].Filename+"\n")

		}

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func main() {
	err := savePidFile(os.Getpid())
	if err != nil {
		log.Fatal(err)
	}

	srv := &http.Server{}
	log.Println("Server started, serving on:", socketType, socket)

	sig_chan := make(chan os.Signal, 1)
	signal.Notify(sig_chan, os.Interrupt, os.Kill, syscall.SIGTERM)
	go func() {
		sigReceived := <-sig_chan
		signal.Stop(sig_chan)
		fmt.Println("Exit command received.", sigReceived)
		srv.Shutdown(nil)
		os.Remove(pfile)
		os.Exit(0)
	}()

	http.HandleFunc(statusUrl, statusHandler)
	http.HandleFunc(uploadUrl, uploadHandler)
	log.Fatal(srv.Serve(listener))
}
