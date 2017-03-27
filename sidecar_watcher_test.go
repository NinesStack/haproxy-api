package main

import (
	"errors"
	"testing"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/relistan/go-director"
	. "github.com/smartystreets/goconvey/convey"
)

func Test_NewSidecarWatcher(t *testing.T) {
	Convey("NewSidecarWatcher properly configured a SidecarWatcher", t, func() {
		log.SetLevel(log.ErrorLevel)

		looper := director.NewFreeLooper(1, make(chan error))
		notifyChan := make(chan struct{})
		watcher := NewSidecarWatcher("http://example.com/watch", looper, notifyChan)

		So(watcher.looper, ShouldEqual, looper)
		So(watcher.notifyChan, ShouldEqual, notifyChan)
		So(watcher.timer, ShouldNotBeNil)
		So(watcher.RefreshConn, ShouldEqual, CONNECTION_REFRESH_TIME)
	})
}

func Test_onChange(t *testing.T) {
	Convey("onChange()", t, func() {
		log.SetLevel(log.ErrorLevel)

		looper := director.NewFreeLooper(1, make(chan error))
		notifyChan := make(chan struct{}, 1)
		watcher := NewSidecarWatcher("http://example.com/watch", looper, notifyChan)

		Convey("returns nil on error", func() {
			err := errors.New("Oh no!")
			err2 := watcher.onChange(nil, err)

			So(len(notifyChan), ShouldEqual, 0)
			So(err2, ShouldBeNil)
		})

		Convey("notifies the channel", func() {
			watcher.onChange(nil, nil)

			So(len(notifyChan), ShouldEqual, 1)
		})

		Convey("resets the Timer", func() {
			watcher.RefreshConn = time.Duration(0)
			watcher.onChange(nil, nil)

			// We know it was reset if it expires right away because it's
			// reset to the value of RefreshConn instead of the default.
			var expired bool
			select {
			case <-watcher.timer.C:
				expired = true
			}

			So(expired, ShouldBeTrue)
		})
	})
}
