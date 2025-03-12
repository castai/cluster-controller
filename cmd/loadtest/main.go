package main

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/castai/cluster-controller/loadtest"
)

// TODO: Move to corba

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := loadtest.Config{
		Port: 8080,
	}

	logger.Info("creating test server")
	// TODO: Defaults...
	testServer := loadtest.NewTestServer(logger, loadtest.TestServerConfig{
		BufferSize:               1000,
		MaxActionsPerCall:        500,
		TimeoutWaitingForActions: 60 * time.Second,
	})

	go func() {
		ch := testServer.GetActionsPushChannel()

		loadtest.TestStuckDrain(ch)
		//for range 10000 {
		//	ch <- castai.ClusterAction{
		//		ID: uuid.NewString(),
		//		ActionCreateEvent: &castai.ActionCreateEvent{
		//			Reporter: "provisioning.cast.ai",
		//			ObjectRef: corev1.ObjectReference{
		//				Kind:       "Pod",
		//				Namespace:  "default",
		//				Name:       "Dummy-pod",
		//				UID:        types.UID(uuid.New().String()),
		//				APIVersion: "v1",
		//			},
		//			EventTime: time.Now(),
		//			EventType: "Warning",
		//			//Reason:    fmt.Sprintf("Just because! %d", i),
		//			Reason:  "Reason",
		//			Action:  "During node creation.",
		//			Message: "Oh common, you can do better.",
		//		},
		//	}
		//}
	}()

	logger.Info(fmt.Sprintf("starting http server on port %d", cfg.Port))
	// TODO: Cleanup
	err := loadtest.NewHttpServer(cfg, testServer)
	if err != nil {
		panic(err)
	}
}
