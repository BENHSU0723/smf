package producer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	ben_models "github.com/BENHSU0723/openapi_public/models"
	"github.com/free5gc/openapi/Nsmf_EventExposure"
	"github.com/free5gc/openapi/models"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/util/httpwrapper"
)

func HandleVn5gGroupMulticastGroupsCreationNotification(notifyItems models.ModificationNotification, groupId string) *httpwrapper.Response {
	logger.ChargingLog.Info("Handle Vn5gGroupMulticastGroupsCreation Notification")

	logger.Vn5gLanLog.Warnln("Internal Group Id: ", groupId)
	problemDetails := vn5gGroupMulticastGroupsCreationNotificationProcedure(notifyItems)
	if problemDetails != nil {
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	} else {
		return httpwrapper.NewResponse(http.StatusNoContent, nil, nil)
	}
}

func vn5gGroupMulticastGroupsCreationNotificationProcedure(notifyItems models.ModificationNotification) *models.ProblemDetails {
	var mulcstGp ben_models.MulticastGroup
	// retrive multicast group data
	// TODO: only support process a multicast group now, but notifyItem.Changes will contain multiple multicast groups``
searchGroupInfo:
	for _, notifyItem := range notifyItems.NotifyItems {
		for _, changeItem := range notifyItem.Changes {
			// logger.Vn5gLanLog.Warnln(changeItem)
			// mulcstGp := changeItem.NewValue.(ben_models.MulticastGroup)
			resBytes, _ := json.Marshal(changeItem.NewValue)
			json.Unmarshal(resBytes, &mulcstGp)
			logger.Vn5gLanLog.Warnln(mulcstGp)
			break searchGroupInfo
		}
	}

	// find local vn group related info by external group id
	var targetGpSubs *ben_models.Vn5gGroupConfigSubscription = nil
	for intGpId, groupSubs := range smf_context.GetSelf().Vn5gGroupCfgSubs {
		if groupSubs.ExternalGroupId == mulcstGp.ExternalGroupId {
			if len(smf_context.GetSelf().Vn5gGroupsMulticastMap[intGpId]) == 0 {
				smf_context.GetSelf().Vn5gGroupsMulticastMap[intGpId] = make([]ben_models.MulticastGroup, 0)
			}
			smf_context.GetSelf().Vn5gGroupsMulticastMap[intGpId] = append(smf_context.GetSelf().Vn5gGroupsMulticastMap[intGpId], mulcstGp)
			targetGpSubs = smf_context.GetSelf().Vn5gGroupCfgSubs[intGpId]
			break
		}
	}
	if targetGpSubs == nil {
		logger.Vn5gLanLog.Errorln("can not find the 5GVN group info accroding to external group ID from multicast grup")
		return &models.ProblemDetails{
			Status: http.StatusInternalServerError,
			Detail: "can not find the 5GVN group info accroding to external group ID from multicast grup",
		}
	}

	// retrive 5g vn group members data
	udmPpClient := smf_context.GetSelf().UdmParaProvisionClient
	vnGpCfg, rsp, err := udmPpClient.VN5GgroupDataCollectionApi.VN5GgroupDataGet(context.Background(), targetGpSubs.ExternalGroupId)
	if err != nil || rsp.StatusCode != http.StatusOK {
		logger.Vn5gLanLog.Errorln("get vn 5glan group members from udm error:", err.Error())
		return &models.ProblemDetails{
			Status: http.StatusInternalServerError,
			Detail: "get vn 5glan group members from udm error",
		}
	}

	// for each members who has build up group used PDU session to create igmp pdr/far
	for _, memberGpsi := range vnGpCfg.Members {
		// skip the ue which does not establish pdu session
		var smContextRef string
		if smContextRef = targetGpSubs.CurrMembers[memberGpsi]; smContextRef == "" {
			continue
		}
		logger.Vn5gLanLog.Warnln("Create IGMP PDR/FAR for ueId:", memberGpsi)

		var smContext *smf_context.SMContext
		if smContext = smf_context.GetSMContextByRef(smContextRef); smContext == nil {
			logger.Vn5gLanLog.Errorf("Can't find the SM context of ue[%s],refId[%s]\n", memberGpsi, smContextRef)
			continue
		} else if smContext.Supi == mulcstGp.SourceUeGpsi {
			logger.Vn5gLanLog.Warnf("skip the source ue(server) to build igmp: supi[%s]", smContext.Supi)
			continue
		}
		// set sm context lock
		smContext.SMLock.Lock()

		// try to get default path, and create IGMP PDR/FAR
		datapath := smContext.Tunnel.DataPathPool.GetDefaultPath()
		logger.Vn5gLanLog.Warnf("get default data path: %+v\n", datapath)
		logger.Vn5gLanLog.Warnf("get first node: %+v\n", *(datapath.FirstDPNode))
		datapath.Activate5glanMcastIgmpPDR(smContext, 234, mulcstGp.GroupIpAddr,
			vnGpCfg.InternalGroupIdentifier, targetGpSubs.ExternalGroupId, mulcstGp.MultiGroupId)

		// put new created PDR/FAR to pfcp session, modify existed pfcp session
		go func() {
			defer smContext.SMLock.Unlock()

			handler := func(smContext *smf_context.SMContext, success bool) {
				logger.Vn5gLanLog.Warnf("Complete add IGMP PDR to PFCP session, ueId[%s], ueIP[%s]\n",
					smContext.Supi, smContext.PDUAddress.To4().String())
			}

			logger.Vn5gLanLog.Warnln("call ActivateUPFSession to add igmp pdr/far/urr")
			ActivateUPFSession(smContext, handler)

			smContext.PostRemoveDataPath()
		}()
	}

	return nil
}

func HandleChargingNotification(chargingNotifyRequest models.ChargingNotifyRequest,
	smContextRef string,
) *httpwrapper.Response {
	logger.ChargingLog.Info("Handle Charging Notification")

	problemDetails := chargingNotificationProcedure(chargingNotifyRequest, smContextRef)
	if problemDetails != nil {
		return httpwrapper.NewResponse(int(problemDetails.Status), nil, problemDetails)
	} else {
		return httpwrapper.NewResponse(http.StatusNoContent, nil, nil)
	}
}

// While receive Charging Notification from CHF, SMF will send Charging Information to CHF and update UPF
// The Charging Notification will be sent when CHF found the changes of the quota file.
func chargingNotificationProcedure(req models.ChargingNotifyRequest, smContextRef string) *models.ProblemDetails {
	if smContext := smf_context.GetSMContextByRef(smContextRef); smContext != nil {
		smContext.SMLock.Lock()
		defer smContext.SMLock.Unlock()
		upfUrrMap := make(map[string][]*smf_context.URR)
		for _, reauthorizeDetail := range req.ReauthorizationDetails {
			rg := reauthorizeDetail.RatingGroup
			logger.ChargingLog.Infof("Force update charging information for rating group %d", rg)
			for _, urr := range smContext.UrrUpfMap {
				chgInfo := smContext.ChargingInfo[urr.URRID]
				if chgInfo.RatingGroup == rg ||
					chgInfo.ChargingLevel == smf_context.PduSessionCharging {
					logger.ChargingLog.Tracef("Query URR (%d) for Rating Group (%d)", urr.URRID, rg)
					upfId := smContext.ChargingInfo[urr.URRID].UpfId
					upfUrrMap[upfId] = append(upfUrrMap[upfId], urr)
				}
			}
		}
		for upfId, urrList := range upfUrrMap {
			upf := smf_context.GetUpfById(upfId)
			if upf == nil {
				logger.ChargingLog.Warnf("Cound not find upf %s", upfId)
				continue
			}
			QueryReport(smContext, upf, urrList, models.TriggerType_FORCED_REAUTHORISATION)
		}
		ReportUsageAndUpdateQuota(smContext)
	} else {
		problemDetails := &models.ProblemDetails{
			Status: http.StatusNotFound,
			Cause:  "CONTEXT_NOT_FOUND",
			Detail: fmt.Sprintf("SM Context [%s] Not Found ", smContextRef),
		}
		return problemDetails
	}

	return nil
}

func HandleSMPolicyUpdateNotify(smContextRef string, request models.SmPolicyNotification) *httpwrapper.Response {
	logger.PduSessLog.Infoln("In HandleSMPolicyUpdateNotify")
	decision := request.SmPolicyDecision
	smContext := smf_context.GetSMContextByRef(smContextRef)

	if smContext == nil {
		logger.PduSessLog.Errorf("SMContext[%s] not found", smContextRef)
		httpResponse := httpwrapper.NewResponse(http.StatusBadRequest, nil, nil)
		return httpResponse
	}

	smContext.SMLock.Lock()
	defer smContext.SMLock.Unlock()

	smContext.CheckState(smf_context.Active)
	// Wait till the state becomes Active again
	// TODO: implement waiting in concurrent architecture

	smContext.SetState(smf_context.ModificationPending)

	// Update SessionRule from decision
	if err := smContext.ApplySessionRules(decision); err != nil {
		// TODO: Fill the error body
		smContext.Log.Errorf("SMPolicyUpdateNotify err: %v", err)
		return httpwrapper.NewResponse(http.StatusBadRequest, nil, nil)
	}

	//TODO: Response data type -
	//[200 OK] UeCampingRep
	//[200 OK] array(PartialSuccessReport)
	//[400 Bad Request] ErrorReport
	if err := smContext.ApplyPccRules(decision); err != nil {
		smContext.Log.Errorf("apply sm policy decision error: %+v", err)
		// TODO: Fill the error body
		return httpwrapper.NewResponse(http.StatusBadRequest, nil, nil)
	}

	smContext.SendUpPathChgNotification("EARLY", SendUpPathChgEventExposureNotification)

	ActivateUPFSession(smContext, nil)

	smContext.SendUpPathChgNotification("LATE", SendUpPathChgEventExposureNotification)

	smContext.PostRemoveDataPath()

	return httpwrapper.NewResponse(http.StatusNoContent, nil, nil)
}

func SendUpPathChgEventExposureNotification(
	uri string, notification *models.NsmfEventExposureNotification,
) {
	configuration := Nsmf_EventExposure.NewConfiguration()
	client := Nsmf_EventExposure.NewAPIClient(configuration)
	_, httpResponse, err := client.
		DefaultCallbackApi.
		SmfEventExposureNotification(context.Background(), uri, *notification)
	if err != nil {
		if httpResponse != nil {
			logger.PduSessLog.Warnf("SMF Event Exposure Notification Error[%s]", httpResponse.Status)
		} else {
			logger.PduSessLog.Warnf("SMF Event Exposure Notification Failed[%s]", err.Error())
		}
		return
	} else if httpResponse == nil {
		logger.PduSessLog.Warnln("SMF Event Exposure Notification Failed[HTTP Response is nil]")
		return
	}
	defer func() {
		if rspCloseErr := httpResponse.Body.Close(); rspCloseErr != nil {
			logger.PduSessLog.Errorf("SmfEventExposureNotification response body cannot close: %+v", rspCloseErr)
		}
	}()
	if httpResponse.StatusCode != http.StatusOK && httpResponse.StatusCode != http.StatusNoContent {
		logger.PduSessLog.Warnf("SMF Event Exposure Notification Failed")
	} else {
		logger.PduSessLog.Tracef("SMF Event Exposure Notification Success")
	}
}
