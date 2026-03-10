package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/kiber-io/apkd/apkd/logging"
	"github.com/kiber-io/apkd/apkd/sources"

	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
)

var logger = logging.Named("tasks")

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
	queue               chan Task
	wg                  sync.WaitGroup
	maxWorkers          int
	progress            *mpb.Progress
	statusBar           *mpb.Bar
	enqueuedTasks       atomic.Int64
	runningTasks        atomic.Int64
	completedTasks      atomic.Int64
	activeDownloadTasks atomic.Int64
	stateMu             sync.RWMutex
	processedPackages   map[string]struct{}
	processedDevelopers map[string]map[string]struct{}
}

func NewTaskQueue(maxWorkers int) *TaskQueue {
	wg := sync.WaitGroup{}
	tq := &TaskQueue{
		queue:               make(chan Task, 100),
		maxWorkers:          maxWorkers,
		progress:            mpb.New(mpb.WithAutoRefresh(), mpb.WithWaitGroup(&wg)),
		processedPackages:   make(map[string]struct{}),
		processedDevelopers: make(map[string]map[string]struct{}),
	}
	tq.statusBar = tq.progress.New(0, mpb.NopStyle(),
		mpb.BarFillerTrim(),
		mpb.PrependDecorators(decor.Any(func(decor.Statistics) string {
			return tq.progressStatusLine()
		})),
	)
	tq.statusBar.SetPriority(1_000_000 + tq.statusBar.ID())
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
	tq.enqueuedTasks.Add(1)
	select {
	case tq.queue <- task:
	default:
		// Prevent worker deadlocks when producers are workers and queue is full.
		go func(t Task) {
			tq.queue <- t
		}(task)
	}
}

func (tq *TaskQueue) Wait() {
	tq.wg.Wait()
	if tq.statusBar != nil {
		tq.statusBar.SetTotal(1, true)
	}
	tq.progress.Wait()
	close(tq.queue)
}

func (tq *TaskQueue) markPackageProcessed(packageName string) {
	tq.stateMu.Lock()
	tq.processedPackages[packageName] = struct{}{}
	tq.stateMu.Unlock()
}

func (tq *TaskQueue) reservePackageIfNew(packageName string) bool {
	tq.stateMu.Lock()
	defer tq.stateMu.Unlock()
	if _, exists := tq.processedPackages[packageName]; exists {
		return false
	}
	tq.processedPackages[packageName] = struct{}{}
	return true
}

func (tq *TaskQueue) reserveDeveloperSource(developerID, sourceName string) bool {
	tq.stateMu.Lock()
	defer tq.stateMu.Unlock()
	sourcesByDeveloper, exists := tq.processedDevelopers[developerID]
	if !exists {
		sourcesByDeveloper = make(map[string]struct{})
		tq.processedDevelopers[developerID] = sourcesByDeveloper
	}
	if _, exists = sourcesByDeveloper[sourceName]; exists {
		return false
	}
	sourcesByDeveloper[sourceName] = struct{}{}
	return true
}

func (tq *TaskQueue) worker() {
	for task := range tq.queue {
		tq.runningTasks.Add(1)
		switch t := task.(type) {
		case PackageTask:
			tq.markPackageProcessed(t.PackageName)
			tq.processPackageTask(t)
		case VersionTask:
			tq.markPackageProcessed(t.Version.PackageName)
			tq.processVersionTask(t)
		default:
			reportError(fmt.Sprintf("Unknown task type: %T", t))
		}
		tq.runningTasks.Add(-1)
		tq.completedTasks.Add(1)
		tq.wg.Done()
	}
}

func (tq *TaskQueue) progressStatusLine() string {
	queued := tq.enqueuedTasks.Load() - tq.runningTasks.Load() - tq.completedTasks.Load()
	if queued < 0 {
		queued = 0
	}
	return fmt.Sprintf(
		"Progress: downloaded %d | in progress %d | queued %d | errors %d",
		downloadSuccessCount.Load(),
		tq.activeDownloadTasks.Load(),
		queued,
		downloadErrorCount.Load(),
	)
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
		mpb.BarRemoveOnComplete(),
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
	if version == (sources.Version{}) || source == nil {
		if len(errs) == 0 {
			reportError(fmt.Sprintf("Package %s not found in active sources", task.PackageName))
		}
		tq.removeBar(bar)
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
		if !tq.reserveDeveloperSource(version.DeveloperId, source.Name()) {
			return
		}
		logger.Logd(fmt.Sprintf("Searching for packages by developer %s at source %s", version.DeveloperId, source.Name()))
		packages, err := source.FindByDeveloper(version.DeveloperId)
		if err != nil {
			reportError(fmt.Sprintf("Error finding packages by developer %s at source %s: %v", version.DeveloperId, source.Name(), err))
			tq.removeBar(bar)
			return
		}
		for _, packageName := range packages {
			if !tq.reservePackageIfNew(packageName) {
				continue
			}
			logger.Logd(fmt.Sprintf("Found package %s by developer %s at source %s", packageName, version.DeveloperId, source.Name()))
			newTask := PackageTask{
				PackageName: packageName,
			}
			bar := tq.progress.AddBar(1,
				mpb.BarRemoveOnComplete(),
				mpb.PrependDecorators(getDecoratorsForTask(newTask, "queued")...),
			)
			newTask.Bar = bar
			p := 5000 + bar.ID()
			bar.SetPriority(p)
			tq.AddTask(newTask)
		}
	}
}

func (tq *TaskQueue) processVersionTask(task VersionTask) {
	bar := tq.progress.AddBar(int64(task.Version.Size),
		mpb.BarQueueAfter(task.Bar),
		mpb.BarRemoveOnComplete(),
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
			reportError(fmt.Sprintf("File type not found for package %s", task.Version.PackageName))
			tq.removeBar(bar)
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
			reportError(fmt.Sprintf("File %s already exists. Use --force to overwrite.", outFile))
			tq.removeBar(bar)
			return
		}
		logger.Logd(fmt.Sprintf("File %s already exists. Removing...", outFile))
		if err := os.Remove(outFile); err != nil {
			reportError(fmt.Sprintf("Error removing existing file %s: %v", outFile, err))
			tq.removeBar(bar)
			return
		}
	}
	logger.Logi(fmt.Sprintf("Downloading package %s from source %s to file %s", task.Version.PackageName, task.Source.Name(), outFile))
	tq.activeDownloadTasks.Add(1)
	defer tq.activeDownloadTasks.Add(-1)
	reader, err := task.Source.Download(task.Version)
	if err != nil {
		reportError(fmt.Sprintf("Error downloading package %s from source %s: %v", task.Version.PackageName, task.Source.Name(), err))
		tq.removeBar(bar)
		return
	}
	progressReader := bar.ProxyReader(reader)
	progressReaderClosed := false
	defer func() {
		if progressReaderClosed {
			return
		}
		if closeErr := progressReader.Close(); closeErr != nil {
			reportError(fmt.Sprintf("Error closing download stream for package %s: %v", task.Version.PackageName, closeErr))
		}
	}()
	source, isRuStore := task.Source.(*sources.RuStore)
	downloadPath := outFile
	if isRuStore {
		downloadPath = outFile + ".download"
		if err := os.Remove(downloadPath); err != nil && !os.IsNotExist(err) {
			reportError(fmt.Sprintf("Error removing existing temporary file %s: %v", downloadPath, err))
			tq.removeBar(bar)
			return
		}
	}

	file, err := os.Create(downloadPath)
	if err != nil {
		reportError(fmt.Sprintf("Error creating file %s: %v", downloadPath, err))
		tq.removeBar(bar)
		return
	}
	if _, err = io.Copy(file, progressReader); err != nil {
		if closeErr := file.Close(); closeErr != nil {
			reportError(fmt.Sprintf("Error closing file %s after write error: %v", downloadPath, closeErr))
		}
		reportError(fmt.Sprintf("Error saving file %s: %v", downloadPath, err))
		tq.removeBar(bar)
		return
	}
	if err := file.Close(); err != nil {
		reportError(fmt.Sprintf("Error closing file %s: %v", downloadPath, err))
		tq.removeBar(bar)
		return
	}
	if err := progressReader.Close(); err != nil {
		reportError(fmt.Sprintf("Error closing download stream for package %s: %v", task.Version.PackageName, err))
		tq.removeBar(bar)
		return
	}
	progressReaderClosed = true
	if isRuStore {
		// workaround for rustore: sometimes it responds with a zip file in which the APK is stored
		err := source.ExtractApkFromZip(downloadPath, outFile)
		if err != nil {
			reportError(fmt.Sprintf("Error extracting APK from zip file %s: %v", downloadPath, err))
			tq.removeBar(bar)
			return
		}

	}
	// Complete the bar even when source-reported size is inaccurate.
	bar.SetTotal(-1, true)
	reportDownloadSuccess()
	logger.Logi(fmt.Sprintf("Package %s downloaded successfully", task.Version.PackageName))
}

func (tq *TaskQueue) removeBar(prevBar *mpb.Bar) {
	if prevBar == nil {
		return
	}
	if prevBar.Aborted() {
		return
	}
	prevBar.Abort(true)
}

func (tq *TaskQueue) findVersion(packageName string, versionCode int) (sources.Version, sources.Source, []sources.Error) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var latestSource sources.Source
	var latestVersion sources.Version
	var sourcesErrors []sources.Error
	logger.Logi(fmt.Sprintf("Searching for package %s in %d sources", packageName, len(activeSources)))
	for _, source := range activeSources {
		wg.Add(1)
		go func(src sources.Source) {
			defer wg.Done()
			version, err := src.FindByPackage(packageName, versionCode)
			if err != nil {
				var appNotFoundError *sources.AppNotFoundError
				if !errors.As(err, &appNotFoundError) {
					reportError(fmt.Sprintf("Error finding package %s at source %s: %v", packageName, src.Name(), err))
					mu.Lock()
					sourcesErrors = append(sourcesErrors, sources.Error{
						SourceName:  src.Name(),
						PackageName: packageName,
						Err:         err,
					})
					mu.Unlock()
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
