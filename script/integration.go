package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

var (
	showlogs    = false
	maxprocs    = 4
	testPattern = regexp.MustCompile(`test/test-([a-z\-]+)\.sh`)
)

func mainIntegration() {
	if maxprocs < 1 {
		maxprocs = 1
	}

	files := testFiles()

	if len(files) == 0 {
		fmt.Println("no tests to run")
		os.Exit(1)
	}

	var wg sync.WaitGroup
	tests := make(chan string, len(files))
	output := make(chan string, len(files))

	for _, file := range files {
		tests <- file
	}

	go printOutput(output)
	for i := 0; i < maxprocs; i++ {
		wg.Add(1)
		go worker(tests, output, &wg)
	}

	close(tests)
	wg.Wait()
	close(output)
	printOutput(output)
}

func runTest(output chan string, test string) {
	out, _ := exec.Command("/bin/bash", test).CombinedOutput()
	output <- strings.TrimSpace(string(out))
}

func printOutput(output <-chan string) {
	for {
		select {
		case out, ok := <-output:
			if !ok {
				return
			}

			fmt.Println(out)
		}
	}
}

func worker(tests <-chan string, output chan string, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		select {
		case testname, ok := <-tests:
			if !ok {
				return
			}
			runTest(output, testname)
		}
	}
}

func testFiles() []string {
	if len(os.Args) < 4 {
		return allTestFiles()
	}

	fileMap := make(map[string]bool)
	for _, file := range allTestFiles() {
		fileMap[file] = true
	}

	files := make([]string, 0, len(os.Args)-3)
	for _, arg := range os.Args {
		fullname := "test/test-" + arg + ".sh"
		if fileMap[fullname] {
			files = append(files, fullname)
		}
	}

	return files
}

func allTestFiles() []string {
	files := make([]string, 0, 100)
	filepath.Walk("test", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !testPattern.MatchString(path) {
			return nil
		}
		files = append(files, path)
		return nil
	})
	return files
}
