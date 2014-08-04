package inigo_test

import (
	"fmt"
	"path/filepath"
	"syscall"

	"github.com/cloudfoundry-incubator/garden/warden"
	"github.com/cloudfoundry-incubator/inigo/fixtures"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/inigo/loggredile"
	"github.com/cloudfoundry-incubator/inigo/world"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"
	"github.com/cloudfoundry/yagnats"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/grouper"

	"github.com/cloudfoundry-incubator/inigo/inigo_server"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	archive_helper "github.com/pivotal-golang/archiver/extractor/test_helper"
)

var _ = Describe("LRP Consistency", func() {
	var plumbing ifrit.Process
	var runtime ifrit.Process

	var natsClient yagnats.NATSClient
	var wardenClient warden.Client

	var fileServerStaticDir string

	var appId string
	var processGuid string

	var desiredAppRequest models.DesireAppRequestFromCC

	BeforeEach(func() {
		appId = factories.GenerateGuid()

		processGuid = factories.GenerateGuid()

		wardenLinux := componentMaker.WardenLinux()
		wardenClient = wardenLinux.NewClient()

		fileServer, dir := componentMaker.FileServer()
		fileServerStaticDir = dir

		natsClient = yagnats.NewClient()

		plumbing = grouper.EnvokeGroup(grouper.RunGroup{
			"etcd":         componentMaker.Etcd(),
			"nats":         componentMaker.NATS(),
			"warden-linux": wardenLinux,
		})

		runtime = grouper.EnvokeGroup(grouper.RunGroup{
			"cc":             componentMaker.FakeCC(),
			"tps":            componentMaker.TPS(),
			"nsync-listener": componentMaker.NsyncListener(),
			"exec":           componentMaker.Executor(),
			"rep":            componentMaker.Rep(),
			"file-server":    fileServer,
			"auctioneer":     componentMaker.Auctioneer(),
			"route-emitter":  componentMaker.RouteEmitter(),
			"converger":      componentMaker.Converger(),
			"router":         componentMaker.Router(),
			"loggregator":    componentMaker.Loggregator(),
		})

		err := natsClient.Connect(&yagnats.ConnectionInfo{
			Addr: componentMaker.Addresses.NATS,
		})
		Ω(err).ShouldNot(HaveOccurred())

		inigo_server.Start(wardenClient)

		archive_helper.CreateZipArchive("/tmp/simple-echo-droplet.zip", fixtures.HelloWorldIndexApp())
		inigo_server.UploadFile("simple-echo-droplet.zip", "/tmp/simple-echo-droplet.zip")

		cp(
			componentMaker.Artifacts.Circuses[componentMaker.Stack],
			filepath.Join(fileServerStaticDir, world.CircusZipFilename),
		)
	})

	AfterEach(func() {
		inigo_server.Stop(wardenClient)

		runtime.Signal(syscall.SIGKILL)
		Eventually(runtime.Wait()).Should(Receive())

		plumbing.Signal(syscall.SIGKILL)
		Eventually(plumbing.Wait()).Should(Receive())
	})

	Context("with an app running", func() {
		var logOutput *gbytes.Buffer
		var stop chan<- bool

		BeforeEach(func() {
			logOutput = gbytes.NewBuffer()

			stop = loggredile.StreamIntoGBuffer(
				componentMaker.Addresses.LoggregatorOut,
				fmt.Sprintf("/tail/?app=%s", appId),
				"App",
				logOutput,
				logOutput,
			)

			desiredAppRequest = models.DesireAppRequestFromCC{
				ProcessGuid:  processGuid,
				DropletUri:   inigo_server.DownloadUrl("simple-echo-droplet.zip"),
				Stack:        componentMaker.Stack,
				Environment:  []models.EnvironmentVariable{{Name: "VCAP_APPLICATION", Value: "{}"}},
				NumInstances: 2,
				Routes:       []string{"route-to-simple"},
				StartCommand: "./run",
				LogGuid:      appId,
			}

			//start the first two instances
			err := natsClient.Publish("diego.desire.app", desiredAppRequest.ToJSON())
			Ω(err).ShouldNot(HaveOccurred())

			Eventually(helpers.RunningLRPInstancesPoller(componentMaker.Addresses.TPS, processGuid), 2*DEFAULT_EVENTUALLY_TIMEOUT).Should(HaveLen(2))
			poller := helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, "route-to-simple")
			Eventually(poller, 2*DEFAULT_EVENTUALLY_TIMEOUT, 1).Should(Equal([]string{"0", "1"}))
		})

		AfterEach(func() {
			close(stop)
		})

		Describe("Scaling an app up", func() {
			BeforeEach(func() {
				desiredAppRequest.NumInstances = 3

				err := natsClient.Publish("diego.desire.app", desiredAppRequest.ToJSON())
				Ω(err).ShouldNot(HaveOccurred())
			})

			It("should scale up to the correct number of instances", func() {
				Eventually(helpers.RunningLRPInstancesPoller(componentMaker.Addresses.TPS, processGuid)).Should(HaveLen(3))

				poller := helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, "route-to-simple")
				Eventually(poller).Should(Equal([]string{"0", "1", "2"}))
			})
		})

		Describe("Scaling an app down", func() {
			Measure("should scale down to the correct number of instancs", func(b Benchmarker) {
				b.Time("scale down", func() {
					desiredAppRequest.NumInstances = 1
					err := natsClient.Publish("diego.desire.app", desiredAppRequest.ToJSON())
					Ω(err).ShouldNot(HaveOccurred())

					Eventually(helpers.RunningLRPInstancesPoller(componentMaker.Addresses.TPS, processGuid)).Should(HaveLen(1))

					poller := helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, "route-to-simple")
					Eventually(poller, DEFAULT_EVENTUALLY_TIMEOUT, 1).Should(Equal([]string{"0"}))
				})

				b.Time("scale up", func() {
					desiredAppRequest.NumInstances = 2
					err := natsClient.Publish("diego.desire.app", desiredAppRequest.ToJSON())
					Ω(err).ShouldNot(HaveOccurred())

					Eventually(helpers.RunningLRPInstancesPoller(componentMaker.Addresses.TPS, processGuid)).Should(HaveLen(2))

					poller := helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, "route-to-simple")
					Eventually(poller, DEFAULT_EVENTUALLY_TIMEOUT, 1).Should(Equal([]string{"0", "1"}))
				})
			}, helpers.RepeatCount())
		})

		Describe("Stopping an app", func() {
			Measure("should stop all instances of the app", func(b Benchmarker) {
				b.Time("stop", func() {
					desiredAppRequest.NumInstances = 0
					err := natsClient.Publish("diego.desire.app", desiredAppRequest.ToJSON())
					Ω(err).ShouldNot(HaveOccurred())

					Eventually(helpers.RunningLRPInstancesPoller(componentMaker.Addresses.TPS, processGuid)).Should(BeEmpty())

					poller := helpers.HelloWorldInstancePoller(componentMaker.Addresses.Router, "route-to-simple")
					Eventually(poller).Should(BeEmpty())
				})

				b.Time("start", func() {
					desiredAppRequest.NumInstances = 2
					err := natsClient.Publish("diego.desire.app", desiredAppRequest.ToJSON())
					Ω(err).ShouldNot(HaveOccurred())

					Eventually(helpers.RunningLRPInstancesPoller(componentMaker.Addresses.TPS, processGuid)).Should(HaveLen(2))
				})
			}, helpers.RepeatCount())
		})
	})
})
