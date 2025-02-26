package almalinux

import (
	"github.com/Azure/dalec/targets/linux/rpm/distro"
)

const (
	v8TargetKey    = "almalinux8"
	dnfCacheNameV8 = "v8-dnf-cache"

	// v8Ref is the image ref used for the base worker image
	v8Ref      = "mcr.microsoft.com/mirror/docker/library/almalinux:8"
	v8FullName = "AlmaLinux 8"
	// v8WorkerContextName is the build context name that can be used to lookup
	v8WorkerContextName = "dalec-almalinux8-worker"
)

var ConfigV8 = &distro.Config{
	ImageRef:   v8Ref,
	ContextRef: v8WorkerContextName,

	CacheName: dnfCacheNameV8,
	CacheDir:  "/var/cache/dnf",

	ReleaseVer:         "8",
	BuilderPackages:    builderPackages,
	BasePackages:       []string{"almalinux-release", "tzdata"},
	RepoPlatformConfig: &defaultPlatformConfig,
	InstallFunc:        distro.DnfInstall,
}
