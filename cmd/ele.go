package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/alist-org/alist/v3/cmd/flags"
	"github.com/alist-org/alist/v3/internal/bootstrap"
	"github.com/alist-org/alist/v3/internal/bootstrap/data"
	"github.com/alist-org/alist/v3/internal/conf"
	amodel "github.com/alist-org/alist/v3/internal/model"
	"github.com/alist-org/alist/v3/internal/op"
	"github.com/alist-org/alist/v3/pkg/utils"
	"github.com/alist-org/alist/v3/server"
	"github.com/alist-org/alist/v3/server/common"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

func ELE_EditConfig(publicHttp bool, jwtSecret string) {
	if flags.Debug || flags.Dev {
		publicHttp = true
		conf.Conf.Log.Enable = true
	} else {
		conf.Conf.Log.Enable = false
	}

	if publicHttp {
		conf.Conf.Scheme.Address = "0.0.0.0"
		conf.Conf.Scheme.HttpPort = 5324
	} else {
		conf.Conf.Scheme.Address = "127.0.0.1"
		conf.Conf.Scheme.HttpPort = 0
	}

	conf.Conf.JwtSecret = jwtSecret
	conf.Conf.TokenExpiresIn = 24 * 365
	conf.Conf.DistDir = filepath.Join(flags.DataDir, "web")
}

func ELE_Run(publicHttp bool, jwtSecret string) (httpPort int, token string, quit chan bool, err error) {
	bootstrap.InitConfig()
	ELE_EditConfig(publicHttp, jwtSecret)
	bootstrap.Log()
	bootstrap.InitDB()
	data.InitData()
	bootstrap.InitIndex()

	bootstrap.LoadStorages()
	bootstrap.InitTaskManager()

	if !flags.Debug && !flags.Dev {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.LoggerWithWriter(log.StandardLogger().Out), gin.RecoveryWithWriter(log.StandardLogger().Out))
	server.Init(r)

	var httpSrv *http.Server
	if conf.Conf.Scheme.HttpPort != -1 {
		httpBase := fmt.Sprintf("%s:%d", conf.Conf.Scheme.Address, conf.Conf.Scheme.HttpPort)
		utils.Log.Infof("start HTTP server @ %s", httpBase)
		httpSrv = &http.Server{Addr: httpBase, Handler: r}
		var listener net.Listener
		listener, err = net.Listen("tcp", httpBase)
		if err != nil {
			utils.Log.Fatalf("failed to start http: %s", err.Error())
			return
		}
		go func() {
			httpPort = listener.Addr().(*net.TCPAddr).Port
			err := httpSrv.Serve(listener)
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				utils.Log.Fatalf("failed to start http: %s", err.Error())
			}
		}()
	}
	s3r := gin.New()
	s3r.Use(gin.LoggerWithWriter(log.StandardLogger().Out), gin.RecoveryWithWriter(log.StandardLogger().Out))
	server.InitS3(s3r)

	quit = make(chan bool, 1)
	go func() {
		<-quit
		utils.Log.Println("Shutdown server...")
		Release()
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		var wg sync.WaitGroup
		if conf.Conf.Scheme.HttpPort != -1 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := httpSrv.Shutdown(ctx); err != nil {
					utils.Log.Fatal("HTTP server shutdown err: ", err)
				}
			}()
		}
		wg.Wait()
		utils.Log.Println("Server exit")
	}()

	var user *amodel.User
	user, err = op.GetUserByName("admin")
	if err != nil {
		quit <- true
		return
	}

	token, err = common.GenerateToken(user)
	if err != nil {
		quit <- true
		return
	}

	return
}
