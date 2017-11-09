package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	clientHTTP "github.com/zhaojkun/client/http"
)

func main() {
	conn, err := net.Dial("tcp", "127.0.0.1:55928")
	if err != nil {
		log.Fatal(err)
	}
	c := clientHTTP.NewClientConn(conn, nil)
	req, _ := http.NewRequest("GET", "/", nil)
	log.Println("time to write 1", c.Ping())
	resp, err := c.Do(req)
	fmt.Println(resp, err)
	io.Copy(os.Stdout, resp.Body)
	var n int
	fmt.Scanf("%d", &n)
	fmt.Println(n)
	log.Println("time to write 2", c.Ping())
	resp, err = c.Do(req)
	fmt.Println(resp, err)
	io.Copy(os.Stdout, resp.Body)
	time.Sleep(time.Minute)
}
