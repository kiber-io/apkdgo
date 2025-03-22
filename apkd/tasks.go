package main

import (
	"fmt"
	"io"
	"kiber-io/apkd/apkd/sources"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
)

type Task any

type PackageTask struct {
	Task
	PackageName string
	VersionCode int
}

type VersionTask struct {
	Task
	Version sources.Version
	Source  sources.Source
	Bar     *mpb.Bar
}

type TaskQueue struct {
	queue             chan Task
	wg                sync.WaitGroup
	maxWorkers        int
	progress          *mpb.Progress
	processedPackages []string
}

func NewTaskQueue(maxWorkers int) *TaskQueue {
	wg := sync.WaitGroup{}
	tq := &TaskQueue{
		queue:      make(chan Task, 100),
		maxWorkers: maxWorkers,
		progress:   mpb.New(mpb.WithAutoRefresh(), mpb.WithWaitGroup(&wg)),
	}

	for range maxWorkers {
		go tq.worker()
	}

	return tq
}

func (tq *TaskQueue) AddTask(task Task) {
	tq.wg.Add(1)
	tq.queue <- task
}

func (tq *TaskQueue) Wait() {
	tq.wg.Wait()
	tq.progress.Wait()
	close(tq.queue)
}

func (tq *TaskQueue) worker() {
	for task := range tq.queue {
		switch t := task.(type) {
		case PackageTask:
			tq.processedPackages = append(tq.processedPackages, t.PackageName)
			tq.processPackageTask(t)
		case VersionTask:
			tq.processedPackages = append(tq.processedPackages, t.Version.PackageName)
			tq.processVersionTask(t)
		default:
			collectedErrors = append(collectedErrors, fmt.Sprintf("Unknown task type: %T", t))
		}
		tq.wg.Done()
	}
}

func getDecoratorsForTask(task Task, status string) []decor.Decorator {
	var decorators []decor.Decorator
	wc := decor.WC{C: decor.DSyncSpaceR}

	switch t := task.(type) {
	case PackageTask:
		decorators = append(decorators, decor.Name(t.PackageName, wc))
		decorators = append(decorators, decor.Name("-", wc))
		if t.VersionCode != 0 {
			decorators = append(decorators, decor.Name(fmt.Sprintf("(%d)", t.VersionCode), wc))
		} else {
			decorators = append(decorators, decor.Name("-", wc))
		}
	case VersionTask:
		decorators = append(decorators, decor.Name(t.Version.PackageName, wc))
		decorators = append(decorators, decor.Name(fmt.Sprintf("v%s", t.Version.Name), wc))
		decorators = append(decorators, decor.Name(fmt.Sprintf("(%d)", t.Version.Code), wc))
		decorators = append(decorators, decor.Name(t.Source.Name(), wc))
	}
	if status != "" {
		decorators = append(decorators, decor.Name("["+status+"]", wc))
	}

	return decorators
}

func (tq *TaskQueue) processPackageTask(task PackageTask) {
	barSearch := tq.progress.AddBar(1,
		mpb.PrependDecorators(getDecoratorsForTask(task, "search")...),
	)
	p := 1000 + barSearch.ID()
	barSearch.SetPriority(p)
	version, source, errs := findVersion(task.PackageName, task.VersionCode)
	for _, err := range errs {
		collectedErrors = append(collectedErrors, fmt.Sprintf("Source: %s, Package: %s, Error: %s", err.SourceName, err.PackageName, err.Err.Error()))
	}
	if version == (sources.Version{}) || source == nil {
		var errorText string
		if len(errs) > 0 {
			errorText = "error"
		} else {
			errorText = "not found"
		}
		tq.showErrorBar(barSearch, task, errorText)
		return
	}
	tq.AddTask(VersionTask{
		Version: version,
		Source:  source,
		Bar:     barSearch,
	})

	if batchDeveloperDownloadMode && version.DeveloperId != "" {
		versions, err := source.FindByDeveloper(version.DeveloperId)
		if err != nil {
			collectedErrors = append(collectedErrors, fmt.Sprintf("Error finding versions by developer %s at source %s: %v", version.DeveloperId, source.Name(), err))
			tq.showErrorBar(barSearch, task, "error")
			return
		}
		for _, version := range versions {
			if !slices.Contains(tq.processedPackages, version.PackageName) {
				newTask := VersionTask{
					Version: version,
					Source:  source,
				}
				bar := tq.progress.AddBar(1,
					mpb.PrependDecorators(getDecoratorsForTask(newTask, "queued")...),
				)
				newTask.Bar = bar
				p := 2000 + bar.ID()
				bar.SetPriority(p)
				tq.AddTask(newTask)
			}
		}
	}
}

func (tq *TaskQueue) processVersionTask(task VersionTask) {
	bar := tq.progress.AddBar(int64(task.Version.Size),
		mpb.BarQueueAfter(task.Bar),
		mpb.PrependDecorators(getDecoratorsForTask(task, "")...),
		mpb.AppendDecorators(
			decor.Percentage(decor.WC{W: 5}),
		),
	)
	if task.Bar != nil {
		task.Bar.Abort(true)
		p := 3000 - task.Bar.ID()
		task.Bar.SetPriority(p)
	} else {
		p := 3000 - bar.ID()
		bar.SetPriority(p)
	}
	var outFile string
	if outputFileName != "" {
		outFile = outputFileName
	} else {
		outFile = fmt.Sprintf("%s-%s-v%d.apk", task.Version.PackageName, task.Version.Name, task.Version.Code)
		outFile = sanitizeFileName(outFile)
	}
	if outputDir != "" {
		outFile = filepath.Join(outputDir, outFile)
	}
	if _, err := os.Stat(outFile); err == nil {
		if !forceDownload {
			collectedErrors = append(collectedErrors, fmt.Sprintf("File %s already exists. Use --force to overwrite.", outFile))
			tq.showErrorBar(bar, task, "error")
			return
		}
		if err := os.Remove(outFile); err != nil {
			collectedErrors = append(collectedErrors, fmt.Sprintf("Error removing existing file %s: %v", outFile, err))
			tq.showErrorBar(bar, task, "error")
			return
		}
	}
	reader, err := task.Source.Download(task.Version)
	if err != nil {
		collectedErrors = append(collectedErrors, fmt.Sprintf("Error downloading package %s from source %s: %v", task.Version.PackageName, task.Source.Name(), err))
		tq.showErrorBar(bar, task, "error")
		return
	}
	progressReader := bar.ProxyReader(reader)
	defer progressReader.Close()
	file, err := os.Create(outFile)
	if err != nil {
		collectedErrors = append(collectedErrors, fmt.Sprintf("Error creating file %s: %v", outFile, err))
		tq.showErrorBar(bar, task, "error")
		return
	}
	defer file.Close()

	if _, err = io.Copy(file, progressReader); err != nil {
		collectedErrors = append(collectedErrors, fmt.Sprintf("Error saving file %s: %v", outFile, err))
		tq.showErrorBar(bar, task, "error")
	}
}

func (tq *TaskQueue) showErrorBar(prevBar *mpb.Bar, task Task, errorText string) {
	barError := tq.progress.AddBar(1,
		mpb.BarQueueAfter(prevBar),
		mpb.PrependDecorators(getDecoratorsForTask(task, errorText)...),
	)
	p := 10000 - prevBar.ID()
	prevBar.SetPriority(p)
	prevBar.Abort(true)
	barError.Abort(false)
}
