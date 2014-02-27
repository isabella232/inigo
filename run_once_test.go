package inigo_test

import (
	"github.com/cloudfoundry-incubator/inigo/executor_runner"
	"github.com/cloudfoundry-incubator/inigo/inigolistener"
	Bbs "github.com/cloudfoundry-incubator/runtime-schema/bbs"
	"github.com/cloudfoundry-incubator/runtime-schema/models/factories"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("RunOnce", func() {
	var bbs *Bbs.BBS

	BeforeEach(func() {
		bbs = Bbs.New(etcdRunner.Adapter())
	})

	Context("when there is an executor running and a RunOnce is registered", func() {
		BeforeEach(func() {
			executorRunner.Start()
		})

		It("eventually runs the RunOnce", func() {
			guid := factories.GenerateGuid()
			runOnce := factories.BuildRunOnceWithRunAction(1, 1, inigolistener.CurlCommand(guid))
			bbs.DesireRunOnce(runOnce)

			Eventually(inigolistener.ReportingGuids, 5.0).Should(ContainElement(guid))
		})
	})

	Context("when there are no executors listening when a RunOnce is registered", func() {
		It("eventually runs the RunOnce once an executor comes up", func() {
			guid := factories.GenerateGuid()
			runOnce := factories.BuildRunOnceWithRunAction(1, 1, inigolistener.CurlCommand(guid))
			bbs.DesireRunOnce(runOnce)

			executorRunner.Start()

			Eventually(inigolistener.ReportingGuids, 5.0).Should(ContainElement(guid))
		})
	})

	Context("when an executor disappears", func() {
		var secondExecutor *executor_runner.ExecutorRunner

		BeforeEach(func() {
			secondExecutor = executor_runner.New(
				executorPath,
				gardenRunner.Network,
				gardenRunner.Addr,
				etcdRunner.NodeURLS(),
				"",
				"",
			)

			executorRunner.Start(executor_runner.Config{MemoryMB: 3, DiskMB: 3, ConvergenceInterval: 1})
			secondExecutor.Start(executor_runner.Config{ConvergenceInterval: 1, HeartbeatInterval: 1})
		})

		It("eventually marks jobs running on that executor as failed", func() {
			guid := factories.GenerateGuid()
			runOnce := factories.BuildRunOnceWithRunAction(1024, 1024, inigolistener.CurlCommand(guid)+"; sleep 10")
			bbs.DesireRunOnce(runOnce)
			Eventually(inigolistener.ReportingGuids, 5.0).Should(ContainElement(guid))

			secondExecutor.KillWithFire()

			Eventually(func() interface{} {
				runOnces, _ := bbs.GetAllCompletedRunOnces()
				return runOnces
			}, 5.0).Should(HaveLen(1))
			runOnces, _ := bbs.GetAllCompletedRunOnces()

			completedRunOnce := runOnces[0]
			Ω(completedRunOnce.Guid).Should(Equal(runOnce.Guid))
			Ω(completedRunOnce.Failed).To(BeTrue())
		})
	})
})
