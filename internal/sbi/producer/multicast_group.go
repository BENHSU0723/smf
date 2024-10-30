package producer

import (
	"context"
	"net/http"
	"slices"

	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
)

func ReportIgmpJoinMulcstGroup(smContext *smf_context.SMContext) {
	logger.Vn5gLanLog.Warnln("Handle ReportIgmpJoinMulcstGroup")

	for _, igmpRp := range smContext.IgmpUrrReport {
		mulcstGpId := smContext.MulcstGroupDatas[igmpRp.UrrId].MulcstGroupID
		intGpId := smContext.InternalGroupId
		extGpId := smf_context.GetSelf().Vn5gGroupCfgSubs[intGpId].ExternalGroupId
		// store 5glan-multicast-group data through udm
		udmPpClient := smf_context.GetSelf().UdmParaProvisionClient

		// retrive group config to build complete vn group config
		OldVnGpCfg, rsp, err := udmPpClient.VN5GgroupDataCollectionApi.VN5GgroupDataGet(context.Background(), extGpId)
		if err != nil || rsp.StatusCode != http.StatusOK {
			logger.Vn5gLanLog.Errorln("get VN 5G Group config err:", err.Error())
			logger.Vn5gLanLog.Errorf("skip the igmp report of internal group Id[%s], external group Id[%s], multicast group Id[%s]",
				smContext.InternalGroupId, extGpId, mulcstGpId)
			continue
		}

		// add the multicast group member
		for mulGpIdx, mulcstGp := range OldVnGpCfg.MulticastGroupList {
			if mulcstGp.MultiGroupId == mulcstGpId {
				// if it's a duplicated member, then skip this report
				if !slices.Contains(mulcstGp.MembersGpsi, smContext.Supi) {
					OldVnGpCfg.MulticastGroupList[mulGpIdx].MembersGpsi = append(OldVnGpCfg.MulticastGroupList[mulGpIdx].MembersGpsi, smContext.Supi)
					_, rsp, err := udmPpClient.VN5GgroupDataCollectionApi.VN5GgroupDataPatch(context.Background(), OldVnGpCfg, extGpId)
					if err != nil || rsp.StatusCode != http.StatusOK {
						logger.Vn5gLanLog.Errorln("add a member to multicast group err: ", err.Error())
					}
				}
				continue
			}

			if mulGpIdx == len(OldVnGpCfg.MulticastGroupList)-1 {
				logger.Vn5gLanLog.Errorln("can not find multicast group by id")
			}
		}
	}
	smContext.IgmpUrrReport = []smf_context.IgmpJoinReport{}
}

// entry function for create/modify multicast group traffic flow PDR/FAR
// duplicate and forward packet
func ProduceMulticstGroupTrafficPdrFar() {
	// TODO: below
	// if: webconsole can grant igmp join, then smf will get notify then call this function to create/modify PDR/FAR
	// else: default accecpt all igmp join, so create/modify PDR/FAR directly called ReportIgmpJoinMulcstGroup()
}
