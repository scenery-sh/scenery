//go:build !darwin && !linux

package main

import (
	"context"
	"errors"
	"os"
)

func harnessReportStop(*os.Process) error {
	return errors.New("report snapshot proof requires Darwin/Linux descriptor and owned child stop support")
}
func harnessReportResume(*os.Process) error {
	return errors.New("report snapshot proof requires Darwin/Linux descriptor and owned child stop support")
}
func observeHarnessReportDescriptors(context.Context, int, []string) ([]harnessReportDescriptor, error) {
	return nil, errors.New("report snapshot proof requires Darwin/Linux descriptor and owned child stop support")
}

func harnessReportSelectedDescriptor(string, os.FileInfo) (harnessReportDescriptor, error) {
	return harnessReportDescriptor{}, errors.New("report snapshot proof requires Darwin/Linux descriptor support")
}
