package main

import (
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

const (
	envSidecar         = "PBM_AGENT_SIDECAR"
	envSidecarSleepSec = "PBM_AGENT_SIDECAR_SLEEP"

	agentCmd = "pbm-agent"
)

func main() {
	var procID int

	l := log.New(os.Stderr, "[entrypoint] ", log.LstdFlags|log.Lmsgprefix)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		s := <-sig
		signal.Stop(sig)

		l.Printf("got %s, shutting down", s)
		if procID != 0 {
			err := syscall.Kill(procID, syscall.SIGTERM)
			l.Printf("kill `%s` (%d): %v", agentCmd, procID, err)
		}
		os.Exit(0)
	}()

	var (
		isSidecar bool
		sleepSec  = 1

		err error
	)
	if isSideStr, ok := os.LookupEnv(envSidecar); ok {
		isSidecar, err = strconv.ParseBool(isSideStr)
		if err != nil {
			l.Printf("error: parsing %s value: %v", envSidecar, err)
		}
	}
	if sleepStr, ok := os.LookupEnv(envSidecarSleepSec); ok {
		sec, err := strconv.Atoi(sleepStr)
		if err != nil {
			l.Printf("error: parsing %s value: %v", envSidecarSleepSec, err)
		} else {
			sleepSec = sec
		}
	}

	var exitCode int
	for {
		cmd := exec.Command(agentCmd, os.Args[1:]...)
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin

		exitCode = 1

		l.Printf("starting `%s`", agentCmd)
		err := cmd.Start()
		if err != nil {
			l.Printf("error: starting `%s`: %v", agentCmd, err)
			if !isSidecar {
				os.Exit(exitCode)
			}
		}

		procID = cmd.Process.Pid

		err = cmd.Wait()
		if err != nil {
			if exErr, ok := err.(*exec.ExitError); ok { //nolint:errorlint
				exitCode = exErr.ExitCode()
			}
		}
		if !isSidecar {
			os.Exit(exitCode)
		}

		l.Printf("`%s` exited with code %d", agentCmd, exitCode)
		l.Printf("restart in %d sec", sleepSec)
		time.Sleep(time.Second * time.Duration(sleepSec))
	}
}
