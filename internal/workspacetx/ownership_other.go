//go:build !(aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris)

package workspacetx

func processOwnerInfo(int) ownerProcessInfo { return ownerProcessInfo{} }
