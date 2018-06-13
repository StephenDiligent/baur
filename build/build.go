package build

import (
	"time"
)

// Result result of a build job
type Result struct {
	Job   *Job
	Error error

	Duration time.Duration
	ExitCode int
	Output   string
}

// Job describes abuild job
type Job struct {
	Directory string
	Command   string
	UserData  interface{}
}

// Builder is an interface for builders
type Builder interface {
	Start()
}