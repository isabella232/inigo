package cell_test

import (
	"os"
	"path/filepath"

	archive_helper "code.cloudfoundry.org/archiver/extractor/test_helper"
	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/inigo/fixtures"
	"code.cloudfoundry.org/inigo/helpers"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"
)

var _ = Describe("Placement Tags", func() {
	var (
		processGuid string
		runtime     ifrit.Process
	)

	BeforeEach(func() {
		processGuid = helpers.GenerateGuid()

		var fileServer ifrit.Runner
		fileServer, fileServerStaticDir := componentMaker.FileServer()
		runtime = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
			{"file-server", fileServer},
			{"rep-with-tag", componentMaker.Rep("-placementTag=inigo-tag")},
			{"auctioneer", componentMaker.Auctioneer()},
		}))

		archive_helper.CreateZipArchive(
			filepath.Join(fileServerStaticDir, "lrp.zip"),
			fixtures.GoServerApp(),
		)
	})

	AfterEach(func() {
		helpers.StopProcesses(runtime)
	})

	It("advertises placement tags in the cell presence", func() {
		presences, err := bbsClient.Cells(logger)
		Expect(err).NotTo(HaveOccurred())

		Expect(presences).To(HaveLen(1))
		Expect(presences[0].PlacementTags).To(Equal([]string{"inigo-tag"}))
	})

	Describe("desired lrps", func() {
		var lrp *models.DesiredLRP

		BeforeEach(func() {
			lrp = helpers.LRPCreateRequestWithPlacementTag(processGuid, []string{"inigo-tag"})
		})

		JustBeforeEach(func() {
			err := bbsClient.DesireLRP(logger, lrp)
			Expect(err).NotTo(HaveOccurred())
		})

		It("succeeds and is running on correct cell", func() {
			lrpFunc := func() string {
				lrpGroups, err := bbsClient.ActualLRPGroupsByProcessGuid(logger, processGuid)
				Expect(err).NotTo(HaveOccurred())
				if len(lrpGroups) == 0 {
					return ""
				}
				lrp, _ := lrpGroups[0].Resolve()

				return lrp.CellId
			}
			Eventually(lrpFunc).Should(MatchRegexp("the-cell-id-.*-0"))
		})

		Context("when no cells are advertising the placement tags", func() {
			BeforeEach(func() {
				lrp = helpers.LRPCreateRequestWithPlacementTag(processGuid, []string{""})
			})

			It("fails and sets a placement error", func() {
				lrpFunc := func() string {
					lrpGroups, err := bbsClient.ActualLRPGroupsByProcessGuid(logger, processGuid)
					Expect(err).NotTo(HaveOccurred())
					if len(lrpGroups) == 0 {
						return ""
					}
					lrp, _ := lrpGroups[0].Resolve()

					return lrp.PlacementError
				}

				Eventually(lrpFunc).Should(Equal("found no compatible cell"))
			})
		})
	})
})
