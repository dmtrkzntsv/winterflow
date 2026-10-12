package metrics

// DiskScan is the result of one hourly disk measurement (docker system df
// plus a walk of each app directory), attached to every sample until the
// next scan replaces it.
type DiskScan struct {
	Docker DockerDF
	// Apps maps an app id to its disk usage.
	Apps map[string]AppDisk
}

// AppDisk is one app's disk usage in bytes. Disk is volumes + app directory
// + container writable layers; Image is the size of the images its
// containers run, reported apart because images can be shared.
type AppDisk struct {
	Disk  float64
	Image float64
}
