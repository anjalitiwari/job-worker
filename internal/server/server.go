// Package server implements the JobWorker gRPC service
package server

import (
	"context"
	"errors"
	"io"
	"sync"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"github.com/anjalitiwari/job-worker/internal/worker"
	pb "github.com/anjalitiwari/job-worker/proto"
)

// Server implements pb.JobWorkerServer.
type Server struct {
	pb.UnimplementedJobWorkerServer
	mu   sync.RWMutex
	jobs map[string]*worker.Job
}

func New() *Server {
	return &Server{jobs: make(map[string]*worker.Job)}
}

func (s *Server) Start(ctx context.Context, req *pb.StartRequest) (*pb.StartResponse, error) {
	if req.GetCommand() == "" {
		return nil, status.Error(codes.InvalidArgument, "command is required")
	}

	j, err := worker.NewJob(req.GetCommand(), req.GetArgs())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "start: %v", err)
	}
	id := uuid.NewString()

	s.mu.Lock()
	s.jobs[id] = j
	s.mu.Unlock()

	return &pb.StartResponse{JobId: id}, nil
}

func (s *Server) Stop(ctx context.Context, req *pb.StopRequest) (*pb.StopResponse, error) {
	j, err := s.lookup(req.GetJobId())
	if err != nil {
		return nil, err
	}
	if err := j.Stop(); err != nil {
		// Already stopped/exited — surface as FailedPrecondition per design doc.
		return nil, status.Errorf(codes.FailedPrecondition, "stop: %v", err)
	}
	return &pb.StopResponse{}, nil
}

func (s *Server) Status(ctx context.Context, req *pb.StatusRequest) (*pb.StatusResponse, error) {
	j, err := s.lookup(req.GetJobId())
	if err != nil {
		return nil, err
	}
	st := j.Status()
	return &pb.StatusResponse{
		JobId:    req.GetJobId(),
		State:    toProtoState(st.State),
		ExitCode: int32(st.ExitCode),
		Pid:      int32(st.PID),
	}, nil
}

func (s *Server) Output(req *pb.OutputRequest, stream pb.JobWorker_OutputServer) error {
	j, err := s.lookup(req.GetJobId())
	if err != nil {
		return err
	}

	r := j.Output()
	defer r.Close()

	buf := make([]byte, 32*1024)
	for {
		// Stop streaming if the client cancels.
		if cerr := stream.Context().Err(); cerr != nil {
			return status.FromContextError(cerr).Err()
		}

		n, err := r.Read(buf)
		if n > 0 {
			if sendErr := stream.Send(&pb.OutputChunk{Data: buf[:n]}); sendErr != nil {
				return sendErr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return status.Errorf(codes.Internal, "output: %v", err)
		}
	}
}

func (s *Server) lookup(id string) (*worker.Job, error) {
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "job_id is required")
	}
	s.mu.RLock()
	j, ok := s.jobs[id]
	s.mu.RUnlock()
	if !ok {
		return nil, status.Error(codes.NotFound, "job not found")
	}
	return j, nil
}

func toProtoState(s worker.JobState) pb.JobState {
	switch s {
	case worker.JobStateRunning:
		return pb.JobState_JOB_STATE_RUNNING
	case worker.JobStateExited:
		return pb.JobState_JOB_STATE_EXITED
	default:
		return pb.JobState_JOB_STATE_UNSPECIFIED
	}
}
