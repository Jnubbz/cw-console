//go:build !linux

package main

import (
	"errors"
	"time"
)

type catConn struct{}

func openCAT(port string, baud int) (*catConn, error) {
	return nil, errors.New("rig control is on the Pi box only")
}
func (c *catConn) write(s string) error                          { return nil }
func (c *catConn) readReply() (string, error)                    { return "", nil }
func (c *catConn) readReplyWithin(time.Duration) (string, error) { return "", nil }
func (c *catConn) Close()                                        {}
