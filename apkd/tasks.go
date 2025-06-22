package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/kiber-io/apkd/apkd/logger"
	"github.com/kiber-io/apkd/apkd/sources"

	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
)

var processedDevelopers = make(map[string][]string)

type Task any

type PackageTask struct {
	Task
	PackageName string
	VersionCode int
	Bar         *mpb.Bar
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
	log.SetOutput(tq.progress)

	for range tq.maxWorkers {
		go tq.worker()
	}

	return tq
}

func (tq *TaskQueue) AddTask(task Task) {
	if pkgTask, ok := task.(PackageTask); ok {
		logger.Logd(fmt.Sprintf("Adding task: %s", pkgTask.PackageName))
	} else if verTask, ok := task.(VersionTask); ok {
		logger.Logd(fmt.Sprintf("Adding task: %s", verTask.Version.PackageName))
	}
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
	bar := tq.progress.AddBar(1,
		mpb.BarQueueAfter(task.Bar),
		mpb.PrependDecorators(getDecoratorsForTask(task, "search")...),
	)
	if task.Bar != nil {
		p := task.Bar.ID() + 3000
		task.Bar.SetPriority(p)
		task.Bar.Abort(true)
	} else {
		p := 3000 - bar.ID()
		bar.SetPriority(p)
	}
	version, source, errs := tq.findVersion(task.PackageName, task.VersionCode)
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
		tq.showErrorBar(bar, task, errorText)
		return
	}
	var wg2 sync.WaitGroup
	wg2.Add(1)
	go func() {
		defer wg2.Done()
		tq.processVersionTask(VersionTask{
			Version: version,
			Source:  source,
			Bar:     bar,
		})
	}()
	defer wg2.Wait()
	if batchDeveloperDownloadMode && version.DeveloperId != "" {
		if _, ok := processedDevelopers[version.DeveloperId]; ok {
			if slices.Contains(processedDevelopers[version.DeveloperId], source.Name()) {
				return
			}
		}
		processedDevelopers[version.DeveloperId] = append(processedDevelopers[version.DeveloperId], source.Name())
		logger.Logd(fmt.Sprintf("Searching for packages by developer %s at source %s", version.DeveloperId, source.Name()))
		packages, err := source.FindByDeveloper(version.DeveloperId)
		if err != nil {
			collectedErrors = append(collectedErrors, fmt.Sprintf("Error finding packages by developer %s at source %s: %v", version.DeveloperId, source.Name(), err))
			tq.showErrorBar(bar, task, "error")
			return
		}
		for _, packageName := range packages {
			if !slices.Contains(tq.processedPackages, packageName) {
				logger.Logd(fmt.Sprintf("Found package %s by developer %s at source %s", packageName, version.DeveloperId, source.Name()))
				newTask := PackageTask{
					PackageName: packageName,
				}
				bar := tq.progress.AddBar(1,
					mpb.PrependDecorators(getDecoratorsForTask(newTask, "queued")...),
				)
				newTask.Bar = bar
				p := 5000 + bar.ID()
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
			decor.Name(" / "),
			decor.EwmaSpeed(decor.SizeB1024(0), "% .2f", 30),
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
		if task.Version.Type == "" {
			collectedErrors = append(collectedErrors, fmt.Sprintf("File type not found for package %s", task.Version.PackageName))
			tq.showErrorBar(bar, task, "error")
			return
		}
		outFile = fmt.Sprintf("%s-%s-v%d.%s", task.Version.PackageName, task.Version.Name, task.Version.Code, task.Version.Type)
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
		logger.Logd(fmt.Sprintf("File %s already exists. Removing...", outFile))
		if err := os.Remove(outFile); err != nil {
			collectedErrors = append(collectedErrors, fmt.Sprintf("Error removing existing file %s: %v", outFile, err))
			tq.showErrorBar(bar, task, "error")
			return
		}
	}
	logger.Logi(fmt.Sprintf("Downloading package %s from source %s to file %s", task.Version.PackageName, task.Source.Name(), outFile))
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
		return
	}
	source, ok := task.Source.(*sources.RuStore)
	if ok {
		file.Close()
		// workaround for rustore: it's respond with zip file in which apk is stored
		err := source.ExtractApkFromZip(outFile)
		if err != nil {
			collectedErrors = append(collectedErrors, fmt.Sprintf("Error extracting APK from zip file %s: %v", outFile, err))
			return
		}

	}
	logger.Logi(fmt.Sprintf("Package %s downloaded successfully", task.Version.PackageName))
}

func (tq *TaskQueue) showErrorBar(prevBar *mpb.Bar, task Task, errorText string) {
	if prevBar.Aborted() {
		return
	}
	barError := tq.progress.AddBar(1,
		mpb.BarQueueAfter(prevBar),
		mpb.PrependDecorators(getDecoratorsForTask(task, errorText)...),
	)
	p := 10000 - prevBar.ID()
	prevBar.SetPriority(p)
	prevBar.Abort(true)
	barError.Abort(false)
}

func (tq *TaskQueue) findVersion(packageName string, versionCode int) (sources.Version, sources.Source, []sources.Error) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var latestSource sources.Source
	var latestVersion sources.Version
	var appNotFoundError *sources.AppNotFoundError
	var sourcesErrors []sources.Error
	logger.Logi(fmt.Sprintf("Searching for package %s in %d sources", packageName, len(activeSources)))
	for _, source := range activeSources {
		wg.Add(1)
		go func(src sources.Source) {
			defer wg.Done()
			version, err := src.FindByPackage(packageName, versionCode)
			if err != nil {
				if !errors.As(err, &appNotFoundError) {
					logger.Loge(fmt.Sprintf("Error finding package %s at source %s: %v", packageName, src.Name(), err))
					sourcesErrors = append(sourcesErrors, sources.Error{
						SourceName:  src.Name(),
						PackageName: packageName,
						Err:         err,
					})
				} else {
					logger.Logd(fmt.Sprintf("Package %s not found at source %s", packageName, src.Name()))
				}
				return
			}
			mu.Lock()
			logger.Logi(fmt.Sprintf("Found package %s v%s (%v) at source %s", packageName, version.Name, version.Code, src.Name()))
			if version.Code > latestVersion.Code {
				latestVersion = version
				latestSource = src
			}
			mu.Unlock()
		}(source)
	}

	wg.Wait()

	return latestVersion, latestSource, sourcesErrors
}
