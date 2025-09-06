package main

import (
	"fmt"
	"net/http"
	"sync"
)

func main() {
	urls, err := GetRandomImageURLs(200, 300, 5)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	for _, url := range urls {
		fmt.Println(url)
	}
}

func GetRandomImageURLs(width, height, amount int) ([]string, error) {
	urls := make([]string, amount)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Semaphore channel to limit concurrency
	maxConcurrency := 20
	sem := make(chan struct{}, maxConcurrency)

	for i := 0; i < amount; i++ {
		wg.Add(1)
		sem <- struct{}{} // Acquire a slot

		go func(index int) {
			defer wg.Done()
			defer func() { <-sem }() // Release slot

			url := fmt.Sprintf("https://picsum.photos/%d/%d", width, height)
			resp, err := client.Get(url)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusFound {
				redirectURL := resp.Header.Get("Location")
				mu.Lock()
				urls[index] = redirectURL
				mu.Unlock()
			} else {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("unexpected status code: %d", resp.StatusCode)
				}
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}
	return urls, nil
}
