package main

import "scenery.sh/internal/devprocess"

var processAliveForEdge = devprocess.Alive
var waitForPIDExit = devprocess.WaitForExit
var inspectProcess = devprocess.Inspect

var commandTreeContext = devprocess.CommandContext
var configureChildProcess = devprocess.ConfigureChild
var configureDetachedChildProcess = devprocess.ConfigureDetachedChild

type devProcessReadyRequest = devprocess.ReadyRequest
type devProcessStartRequest = devprocess.StartRequest
var killProcessIDTree = devprocess.KillTreePID
var killProcessTree = devprocess.KillTree

var startDevManagedProcess = devprocess.Start
