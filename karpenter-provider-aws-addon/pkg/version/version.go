package version

import "k8s.io/apimachinery/pkg/version"

var (
	gitVersion = "0.1.0"
	gitCommit  = "unknown"
	gitMajor   = ""
	gitMinor   = ""
	buildDate  = ""
)

func Get() version.Info {
	return version.Info{
		Major:      gitMajor,
		Minor:      gitMinor,
		GitCommit:  gitCommit,
		GitVersion: gitVersion,
		BuildDate:  buildDate,
	}
}
