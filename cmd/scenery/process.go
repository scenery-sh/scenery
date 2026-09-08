package main

import "scenery.sh/internal/devprocess"

func lockManagedSubstrateRoot(root, kind string) (func(), error) {
	return devprocess.LockSubstrate(root, kind, devprocess.LockOptions{})
}

var processAliveForEdge = devprocess.Alive
var waitForPIDExit = devprocess.WaitForExit

func inspectProcess(pid int) (procInfo, bool) {
	info, ok := devprocess.Inspect(pid)
	return procInfo{pid: info.PID, ppid: info.PPID, stat: info.State, cmd: info.Command}, ok
}

var commandTreeContext = devprocess.CommandContext
var configureDetachedChildProcess = devprocess.ConfigureDetachedChild

type devManagedProcess = devprocess.ManagedProcess
type devProcessReadyRequest = devprocess.ReadyRequest
type devProcessStartRequest = devprocess.StartRequest
var interruptProcessTree = devprocess.InterruptTree
var isExpectedExit = devprocess.IsExpectedExit
var killProcessTree = devprocess.KillTree

const managedFrontendStartupTimeout = devprocess.DefaultStartupTimeout

type safeLineTail = devprocess.LineTail

var startDevManagedProcess = devprocess.Start

const stopTimeout = devprocess.DefaultStopTimeout

var newLineTail = devprocess.NewLineTail
