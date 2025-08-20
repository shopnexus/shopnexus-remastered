package main

import (
	"fmt"
	"os"
	"time"
)

func main() {
	a := make(chan bool)
	print(a)

	go func() {
		time.Sleep(1 * time.Second)
		close(a)
		time.Sleep(1 * time.Second)
		a = make(chan bool)
		print(a)
	}()

	go func() {
		fmt.Printf("hello")
		<-a
		fmt.Println("nigga")
	}()

	select {
	case <-time.After(5 * time.Second):
		os.Exit(1)
	}

}
