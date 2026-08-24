//go:build !(aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris || windows)

package workspacetx

func processOwnerInfo(int) ownerProcessInfo { return ownerProcessInfo{} }
