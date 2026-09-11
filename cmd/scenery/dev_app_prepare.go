package main

import "errors"

// Candidate bytes and runtime capabilities are independent after a successful
// build. Join both before preflight, including on errors; an abandoned copy
// must not outlive its preparation or remove the serving generation's binary.
func prepareAppStartInputs(retain func() (string, error), resolve func() (*devRuntimeEnvironment, error), discard func(string)) (string, *devRuntimeEnvironment, error) {
	type retained struct {
		path string
		err  error
	}
	done := make(chan retained, 1)
	go func() {
		path, err := retain()
		done <- retained{path, err}
	}()
	environment, environmentErr := resolve()
	binary := <-done
	if err := errors.Join(environmentErr, binary.err); err != nil {
		if binary.path != "" {
			discard(binary.path)
		}
		return "", nil, err
	}
	return binary.path, environment, nil
}
