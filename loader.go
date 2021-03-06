package baur

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/simplesurance/baur/v1/cfg"
	"github.com/simplesurance/baur/v1/internal/fs"
)

type Logger interface {
	Debugf(format string, v ...interface{})
}

// Loader discovers and instantiates apps and tasks.
type Loader struct {
	logger          Logger
	includeDB       *cfg.IncludeDB
	repositoryRoot  string
	appConfigPaths  []string
	gitCommitIDFunc func() (string, error)
}

// NewLoader instantiates a Loader.
// When an app config is loaded the DefaultResolvers are applied on the content
// before they are merged with their includes.  The gitCommitIDFunc is used as
// config resolved to resolve $GITCOMMIT variables.
func NewLoader(repoCfg *cfg.Repository, gitCommitIDFunc func() (string, error), logger Logger) (*Loader, error) {
	repositoryRootDir := filepath.Dir(repoCfg.FilePath())

	appConfigPaths, err := findAppConfigs(fs.AbsPaths(repositoryRootDir, repoCfg.Discover.Dirs), repoCfg.Discover.SearchDepth)
	if err != nil {
		return nil, fmt.Errorf("discovering application config files failed: %w", err)
	}

	logger.Debugf("loader: found the following application configs:\n%s", strings.Join(appConfigPaths, "\n"))

	return &Loader{
		logger:          logger,
		repositoryRoot:  repositoryRootDir,
		includeDB:       cfg.NewIncludeDB(logger.Debugf),
		appConfigPaths:  appConfigPaths,
		gitCommitIDFunc: gitCommitIDFunc,
	}, nil
}

// LoadTasks loads the tasks of apps that match the passed specifier.
// Specifier format is <APP-SPEC>[.<TASK-SPEC>].
// <APP-SPEC> is:
//   - <APP-NAME> or
//   - '*'
// <TASK-SPEC> is:
//   - Task Name or
//   - '*'
// If no specifier is passed all tasks of all apps are returned.
// If multiple specifiers match the same task, it's only returned 1x in the returned slice.
func (a *Loader) LoadTasks(specifier ...string) ([]*Task, error) {
	var result []*Task

	specs, err := parseSpecs(specifier)
	if err != nil {
		return nil, err
	}

	specs.all = specs.all || len(specifier) == 0

	apps, err := a.apps(specs)
	if err != nil {
		return nil, err
	}

	if specs.all {
		return a.allTasks(apps), nil
	}

	result = a.allTasks(apps)

	tasks, err := a.tasks(specs.taskSpecs)
	if err != nil {
		return nil, err
	}
	result = append(result, tasks...)

	return dedupTasks(result), nil
}

// LoadApps loads the apps that match the passed specifiers.
// Valid specifiers are:
// - application directory path
// - <APP-NAME>
// - '*'
// If no specifier is passed all apps are returned.
// If multiple specifiers match the same app, it's only returned 1x in the returned slice.
func (a *Loader) LoadApps(specifier ...string) ([]*App, error) {
	specs, err := parseSpecs(specifier)
	if err != nil {
		return nil, err
	}

	if len(specs.taskSpecs) > 0 {
		return nil, fmt.Errorf("invalid app specifiers: %s", specs.taskSpecs)
	}

	specs.all = specs.all || len(specifier) == 0

	return a.apps(specs)
}

// AppNames discovers and loads the apps with the given names.
func (a *Loader) AppNames(names ...string) ([]*App, error) {
	namesMap := make(map[string]struct{}, len(names))
	result := make([]*App, 0, len(names))

	a.logger.Debugf("loader: loading app %q", names)

	for _, name := range names {
		namesMap[name] = struct{}{}
	}

	for _, path := range a.appConfigPaths {
		if len(namesMap) == 0 {
			return result, nil
		}

		path, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}

		appCfg, err := cfg.AppFromFile(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}

		if _, exist := namesMap[appCfg.Name]; !exist {
			continue
		}

		app, err := a.fromCfg(appCfg)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}

		result = append(result, app)

		delete(namesMap, appCfg.Name)
	}

	notFoundApps := make([]string, 0, len(namesMap))
	for name := range namesMap {
		notFoundApps = append(notFoundApps, name)
	}

	if len(notFoundApps) != 0 {
		return nil, fmt.Errorf("could not find the following apps: %s", strings.Join(notFoundApps, ", "))
	}

	return result, nil
}

func (a *Loader) allApps() ([]*App, error) {
	a.logger.Debugf("loader: loading all apps")

	result := make([]*App, 0, len(a.appConfigPaths))

	for _, path := range a.appConfigPaths {
		app, err := a.AppPath(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}

		result = append(result, app)
	}

	return result, nil
}

func (a *Loader) allTasks(apps []*App) []*Task {
	var result []*Task

	for _, app := range apps {
		result = append(result, app.Tasks()...)
	}

	return result
}

// AppDirs load apps from the given directories.
func (a *Loader) AppDirs(dirs ...string) ([]*App, error) {
	result := make([]*App, 0, len(dirs))

	for _, dir := range dirs {
		cfgPath := filepath.Join(dir, AppCfgFile)

		app, err := a.AppPath(cfgPath)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", cfgPath, err)
		}

		result = append(result, app)
	}

	return result, nil
}

// AppPath loads the app from the config file.
func (a *Loader) AppPath(appConfigPath string) (*App, error) {
	a.logger.Debugf("loader: loading app from %q", appConfigPath)

	appConfigPath, err := filepath.Abs(appConfigPath)
	if err != nil {
		return nil, err
	}

	appCfg, err := cfg.AppFromFile(appConfigPath)
	if err != nil {
		return nil, err
	}

	return a.fromCfg(appCfg)
}

// tasks load all tasks for the given taskSpecs.
// wildcards are  only supported for appNames.
func (a *Loader) tasks(taskSpecs []*taskSpec) ([]*Task, error) {
	result := make([]*Task, 0, len(taskSpecs))
	taskSpecMap := make(map[string][]string, len(taskSpecs))
	appNames := make([]string, 0, len(taskSpecs))

	for _, t := range taskSpecs {
		val, exist := taskSpecMap[t.appName]
		if exist {
			taskSpecMap[t.appName] = append(val, t.taskName)
			continue
		}

		appNames = append(appNames, t.appName)
		taskSpecMap[t.appName] = []string{t.taskName}
	}

	var apps []*App
	var err error
	if _, exist := taskSpecMap["*"]; exist {
		apps, err = a.allApps()
	} else {
		apps, err = a.AppNames(appNames...)
	}
	if err != nil {
		return nil, err
	}

	for _, app := range apps {
		taskSpecs := taskSpecMap[app.Name]
		taskSpecs = append(taskSpecs, taskSpecMap["*"]...)

		if len(taskSpecs) == 0 {
			panic(fmt.Sprintf("app %q was loaded which was not part of taskSpecs: %v", app.Name, taskSpecs))
		}

		for _, spec := range taskSpecs {
			for _, task := range app.Tasks() {
				if task.Name == spec {
					result = append(result, task)
				}
			}
		}
	}

	return result, nil
}

func (a *Loader) apps(specs *specs) ([]*App, error) {
	if specs.all {
		return a.allApps()
	}

	result := make([]*App, 0, len(specs.appDirs)+len(specs.appNames))

	for _, path := range specs.appDirs {
		apps, err := a.AppDirs(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}

		result = append(result, apps...)
	}

	apps, err := a.AppNames(specs.appNames...)
	if err != nil {
		return nil, err
	}

	result = append(result, apps...)

	return dedupApps(result), nil
}

func (a *Loader) fromCfg(appCfg *cfg.App) (*App, error) {
	includeResolvers := IncludeCfgVarResolvers(a.repositoryRoot, appCfg.Name)

	err := appCfg.Merge(a.includeDB, includeResolvers)
	if err != nil {
		return nil, fmt.Errorf("merging includes failed: %w", err)
	}

	resolvers := DefaultAppCfgResolvers(a.repositoryRoot, appCfg.Name, a.gitCommitIDFunc)
	err = appCfg.Resolve(resolvers)
	if err != nil {
		return nil, fmt.Errorf("resolving variables in config failed: %w", err)
	}

	err = appCfg.Validate()
	if err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	app, err := NewApp(appCfg, a.repositoryRoot)
	if err != nil {
		return nil, err
	}

	return app, nil
}

// IsAppDirectory returns true if the directory contains an app config file.
func isAppDirectory(dir string) bool {
	cfgPath := filepath.Join(dir, AppCfgFile)
	isFile, _ := fs.IsFile(cfgPath)

	return isFile
}

func findAppConfigs(searchDirs []string, searchDepth int) ([]string, error) {
	var result []string

	for _, searchDir := range searchDirs {
		if err := fs.DirsExist(searchDir); err != nil {
			return nil, fmt.Errorf("application search directory: %w", err)
		}

		cfgPaths, err := fs.FindFilesInSubDir(searchDir, AppCfgFile, searchDepth)
		if err != nil {
			return nil, err
		}

		result = append(result, cfgPaths...)
	}

	return result, nil
}

func dedupApps(apps []*App) []*App {
	dedupMap := make(map[string]*App, len(apps))

	for _, app := range apps {
		if _, exist := dedupMap[app.Path]; exist {
			continue
		}

		dedupMap[app.Path] = app
	}

	result := make([]*App, 0, len(dedupMap))

	for _, app := range dedupMap {
		result = append(result, app)
	}

	return result
}

func dedupTasks(tasks []*Task) []*Task {
	dedupMap := make(map[string]*Task, len(tasks))

	for _, task := range tasks {
		if _, exist := dedupMap[task.ID()]; exist {
			continue
		}

		dedupMap[task.ID()] = task
	}

	result := make([]*Task, 0, len(dedupMap))

	for _, task := range dedupMap {
		result = append(result, task)
	}

	return result
}
