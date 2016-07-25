package main

import log "github.com/Sirupsen/logrus"
import "github.com/gin-gonic/gin"
import "net/http"
import _ "time"
import "io"
import "io/ioutil"
import "os"
import "archive/tar"
import "bytes"
import "path"
import "github.com/docker/engine-api/client"
import "github.com/docker/engine-api/types"
import "github.com/docker/engine-api/types/container"
import "golang.org/x/net/context"

const DOCKER_ENDPOINT = "unix:///var/run/docker.sock"
const DOCKER_VERSION = "v1.18"

func newWorker() (w worker, err error) {
	headers := map[string]string{
		"User-Agent": "engine-api",
	}
	w.c, err = client.NewClient(DOCKER_ENDPOINT, DOCKER_VERSION, nil, headers)
	return w, err
}

type worker struct {
	c  *client.Client
	id string
}

func (w *worker) Create() (err error) {
	config := container.Config{
		Image: "build",
		Cmd:   []string{"bash", "/build.bash"},
	}

	c, err := w.c.ContainerCreate(context.Background(), &config, nil, nil, "")
	if err != nil {
		return err
	}

	w.id = c.ID
	log.Info(w.id, ": created")
	return nil
}

func archive(src string) (*bytes.Reader, error) {
	buf := new(bytes.Buffer)
	tw := tar.NewWriter(buf)

	content, err := ioutil.ReadFile(src)
	if err != nil {
		return nil, err
	}

	h := &tar.Header{
		Name: path.Base(src),
		Mode: 0755,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(h); err != nil {
		return nil, err
	}
	if _, err := tw.Write(content); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}

	return bytes.NewReader(buf.Bytes()), nil
}

func (w *worker) Copy(src string, dst string) error {
	r, err := archive(src)
	if err != nil {
		return err
	}
	if err := w.c.CopyToContainer(context.Background(), w.id, "/", r, types.CopyToContainerOptions{}); err != nil {
		return err
	}
	log.Info(w.id, ": copy ", src, " => ", dst)
	return nil
}

func (w *worker) Start() error {
	log.Info(w.id, ": start")
	return w.c.ContainerStart(context.Background(), w.id, types.ContainerStartOptions{})
}

func main() {
	log.Info("starting build server")

	w, err := newWorker()
	if err != nil {
		log.Fatal(err)
	}

	r := gin.Default()

	r.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"msg": "hello"})
	})

	r.GET("/docker/exec", func(c *gin.Context) {
		if err := w.Create(); err != nil {
			log.Fatal(err)
		}

		if err := w.Copy("./script/build.bash", "/"); err != nil {
			log.Fatal(err)
		}

		if err := w.Start(); err != nil {
			log.Fatal(err)
		}
	})

	r.POST("/tar", func(c *gin.Context) {
		// get file
		file, header, err := c.Request.FormFile("f")
		if err != nil {
			log.Fatal(err)
		}
		log.Info("filename: ", header.Filename)

		// create tar ball reader
		buf := bytes.NewBuffer(nil)
		io.Copy(buf, file)
		tr := tar.NewReader(buf)

		// read files
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				log.Fatal(err)
			}
			log.Infof("Contents of %s:\n", hdr.Name)
			if _, err := io.Copy(os.Stdout, tr); err != nil {
				log.Fatal(err)
			}
		}
	})

	r.POST("/upload", func(c *gin.Context) {
		// get file
		file, header, err := c.Request.FormFile("f")
		if err != nil {
			log.Fatalln(err)
		}
		filename := header.Filename
		log.Info("filename: ", filename)

		// create tmpdir
		tmpdir, err := ioutil.TempDir("", "build")
		if err != nil {
			log.Fatalln(err)
		}
		path := tmpdir + "/" + filename
		log.Info("saved into " + path)
		out, err := os.Create(path)
		defer out.Close()

		// save into tmpdir
		_, err = io.Copy(out, file)
		if err != nil {
			log.Fatalln(err)
		}

		c.JSON(http.StatusOK, gin.H{"msg": "uploaded"})
	})

	r.Run()
}
