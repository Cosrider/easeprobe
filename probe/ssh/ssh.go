/*
 * Copyright (c) 2022, MegaEase
 * All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package ssh

import (
	"bytes"
	"fmt"

	"github.com/megaease/easeprobe/global"
	"github.com/megaease/easeprobe/probe"
	"github.com/megaease/easeprobe/probe/base"
	"golang.org/x/crypto/ssh"

	log "github.com/sirupsen/logrus"
)

// Kind is the type of probe
const Kind string = "ssh"

// Server implements a config for ssh command
type Server struct {
	base.DefaultOptions `yaml:",inline"`
	Endpoint            `yaml:",inline"`
	Command             string   `yaml:"cmd"`
	Args                []string `yaml:"args,omitempty"`
	Env                 []string `yaml:"env,omitempty"`
	Contain             string   `yaml:"contain,omitempty"`
	NotContain          string   `yaml:"not_contain,omitempty"`

	BastionID string    `yaml:"bastion"`
	bastion   *Endpoint `yaml:"-"`
}

// SSH is the SSH probe Configuration
type SSH struct {
	Bastion *BastionMapType `yaml:"bastion"`
	Servers []Server        `yaml:"servers"`
}

// BastionMapType is the type of bastion
type BastionMapType map[string]Endpoint

// BastionMap is a map of bastion
var BastionMap BastionMapType

// ParseAllBastionHost parse all bastion host
func (bm *BastionMapType) ParseAllBastionHost() {
	for k, v := range *bm {
		err := v.ParseHost()
		if err != nil {
			log.Errorf("Bastion Host error: [%s / %s] - %v", k, BastionMap[k].Host, err)
			delete(BastionMap, k)
			continue
		}
		(*bm)[k] = v
	}
}

// Config SSH Config Object
func (s *Server) Config(gConf global.ProbeSettings) error {

	kind := "ssh"
	tag := ""
	name := s.ProbeName
	endpoint := probe.CommandLine(s.Command, s.Args)

	return s.Configure(gConf, kind, tag, name, endpoint, &BastionMap, s.DoProbe)

}

// Configure configure the SSH probe
func (s *Server) Configure(gConf global.ProbeSettings,
	kind, tag, name, endpoint string,
	bastionMap *BastionMapType, fn base.ProbeFuncType) error {

	s.DefaultOptions.Config(gConf, kind, tag, name, endpoint, fn)

	if len(s.Password) <= 0 && len(s.PrivateKey) <= 0 {
		return fmt.Errorf("password or private key is required")
	}

	if len(s.BastionID) > 0 {
		if bastion, ok := (*bastionMap)[s.BastionID]; ok {
			log.Debugf("[%s / %s] - has the bastion [%s]", s.ProbeKind, s.ProbeName, bastion.Host)
			s.bastion = &bastion
		} else {
			log.Warnf("[%s / %s] - wrong bastion [%s]", s.ProbeKind, s.ProbeName, s.BastionID)
		}
	}

	if err := s.Endpoint.ParseHost(); err != nil {
		return err
	}

	log.Debugf("[%s] configuration: %+v, %+v", s.ProbeKind, s, s.Result())
	return nil
}

// DoProbe return the checking result
func (s *Server) DoProbe() (bool, string) {

	output, err := s.RunSSHCmd()

	status := true
	message := "SSH Command has been Run Successfully!"

	if err != nil {
		log.Errorf("[%s / %s] %v", s.ProbeKind, s.ProbeName, err)
		status = false
		message = err.Error() + " - " + output
	}

	log.Debugf("[%s / %s] - %s", s.ProbeKind, s.ProbeName, probe.CommandLine(s.Command, s.Args))
	log.Debugf("[%s / %s] - %s", s.ProbeKind, s.ProbeName, probe.CheckEmpty(string(output)))

	if err := probe.CheckOutput(s.Contain, s.NotContain, string(output)); err != nil {
		log.Errorf("[%s / %s] - %v", s.ProbeKind, s.ProbeName, err)
		message = fmt.Sprintf("Error: %v", err)
		status = false
	}

	return status, message
}

// SetBastion set the bastion
func (s *Server) SetBastion(b *Endpoint) {
	if err := b.ParseHost(); err != nil {
		log.Errorf("[%s / %s] - %v", s.ProbeKind, s.ProbeName, err)
		return
	}
	s.bastion = b
}

// GetSSHClient returns a ssh.Client
func (s *Server) GetSSHClient() error {
	config, err := s.Endpoint.SSHConfig(s.ProbeKind, s.ProbeName, s.Timeout())
	if err != nil {
		return err
	}

	// Connect to the remote server and perform the SSH handshake.
	client, err := ssh.Dial("tcp", s.Host, config)
	if err != nil {
		return err
	}

	s.client = client
	return nil
}

// GetSSHClientFromBastion returns a ssh.Client via bastion server
func (s *Server) GetSSHClientFromBastion() error {
	bConfig, err := s.bastion.SSHConfig(s.ProbeKind, s.ProbeName, s.Timeout())
	if err != nil {
		return fmt.Errorf("Bastion: %s", err)
	}

	bClient, err := ssh.Dial("tcp", s.bastion.Host, bConfig)
	if err != nil {
		return fmt.Errorf("Bastion: %s", err)
	}
	s.bastion.client = bClient

	config, err := s.Endpoint.SSHConfig(s.ProbeKind, s.ProbeName, s.Timeout())
	if err != nil {
		return fmt.Errorf("Server: %s", err)
	}

	// Connect to the remote server and perform the SSH handshake.
	conn, err := bClient.Dial("tcp", s.Host)
	if err != nil {
		return fmt.Errorf("Server: %s", err)
	}

	ncc, chans, reqs, err := ssh.NewClientConn(conn, s.Host, config)
	if err != nil {
		return fmt.Errorf("Server: %s", err)
	}

	s.client = ssh.NewClient(ncc, chans, reqs)
	return nil
}

// RunSSHCmd run ssh command
func (s *Server) RunSSHCmd() (string, error) {

	if s.bastion != nil && len(s.bastion.Host) > 0 {
		if err := s.GetSSHClientFromBastion(); err != nil {
			return "", err
		}
		defer s.bastion.client.Close()
	} else {
		if err := s.GetSSHClient(); err != nil {
			return "", err
		}
	}
	defer s.client.Close()

	// Create a session.
	session, err := s.client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	// Set up environment variables
	env := ""
	for _, e := range s.Env {
		env += "export " + e + ";"
	}

	// Creating the buffer which will hold the remotely executed command's output.
	var stdoutBuf, stderrBuf bytes.Buffer
	session.Stdout = &stdoutBuf
	session.Stderr = &stderrBuf
	if err := session.Run(env + probe.CommandLine(s.Command, s.Args)); err != nil {
		return stderrBuf.String(), err
	}

	return stdoutBuf.String(), nil

}
