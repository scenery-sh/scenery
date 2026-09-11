package main

import (
	"context"
	"io"
	"sort"
	"sync"
)

// StartPrepared runs at most two distinct helper handshakes after the app owns
// their MCP listeners, joining every attempt before readiness. Callers hold
// lifecycle, excluding retries, helper watches and competing stage activation.
func (s *assistantSupervisor) StartPrepared(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	addresses := make([]string, 0, len(s.prepared))
	for address, prepared := range s.prepared {
		if prepared.hasDescriptor() && s.instances[address] == nil {
			addresses = append(addresses, address)
		}
	}
	s.mu.Unlock()
	sort.Strings(addresses)
	jobs := make(chan string)
	var workers sync.WaitGroup
	for range min(2, len(addresses)) {
		workers.Go(func() {
			for address := range jobs {
				s.mu.Lock()
				prepared := s.prepared[address]
				s.mu.Unlock()
				if err := s.startPreparedDefinition(ctx, prepared); err != nil {
					// Preserve independent availability policy; a retained live
					// process prevents scheduleRestart from starting a duplicate.
					s.scheduleRestart(prepared.definition)
				}
			}
		})
	}
	for _, address := range addresses {
		jobs <- address
	}
	close(jobs)
	workers.Wait()
	return nil
}

func (s *assistantSupervisor) reportProcess(name string, pid int) {
	if s.config.OnProcess != nil {
		s.callbacks.Lock()
		defer s.callbacks.Unlock()
		s.config.OnProcess(name, pid)
	}
}

// Stdout and stderr may share a writer. Serialize both across all helpers,
// independently of callbacks that may themselves report output.
type assistantOutputWriter struct {
	mu     *sync.Mutex
	writer io.Writer
}

func (w assistantOutputWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(data)
}

func (s *assistantSupervisor) outputWriter(writer io.Writer) io.Writer {
	if writer == nil {
		return nil
	}
	return assistantOutputWriter{mu: &s.output, writer: writer}
}
