// 临时 TCP 转发器（不入库）：127.0.0.1:7789 → 127.0.0.1:7790（桌面 e2e 截图用，
// 前端硬编码连 7789；验收后删除）。
package main

import (
	"io"
	"log"
	"net"
)

func main() {
	l, err := net.Listen("tcp", "127.0.0.1:7789")
	if err != nil {
		log.Fatal(err)
	}
	log.Println("forwarding 7789 -> 7790")
	for {
		c, err := l.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go func(c net.Conn) {
			defer c.Close()
			up, err := net.Dial("tcp", "127.0.0.1:7790")
			if err != nil {
				log.Println("dial up:", err)
				return
			}
			defer up.Close()
			go io.Copy(up, c)
			io.Copy(c, up)
		}(c)
	}
}
