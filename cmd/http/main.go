package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"

	"github.com/zhaojkun/client/httpclientutil"
)

func main() {
	conn, err := net.Dial("tcp", "127.0.0.1:55928")
	if err != nil {
		log.Fatal(err)
	}
	c := httpclientutil.NewClientConn(conn, nil)
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
	fmt.Println("bye")
}
