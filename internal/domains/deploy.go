package domains

import (
	"certimate/internal/applicant"
	"certimate/internal/deployer"
	"certimate/internal/utils/app"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/pocketbase/pocketbase/models"
)

type Phase string

const (
	checkPhase  Phase = "check"
	applyPhase  Phase = "apply"
	deployPhase Phase = "deploy"
)

func deploy(ctx context.Context, record *models.Record) error {

	currRecord, err := app.GetApp().Dao().FindRecordById("domains", record.Id)
	history := NewHistory(record)
	defer history.commit()
	// ############1.检查域名配置
	history.record(checkPhase, "开始检查", nil)

	if err != nil {
		app.GetApp().Logger().Error("获取记录失败", "err", err)
		history.record(checkPhase, "获取域名配置失败", err)
		return err
	}
	history.record(checkPhase, "获取记录成功", nil)
	if errs := app.GetApp().Dao().ExpandRecord(currRecord, []string{"access", "targetAccess"}, nil); len(errs) > 0 {

		errList := make([]error, 0)
		for name, err := range errs {
			errList = append(errList, fmt.Errorf("展开记录失败,%s: %w", name, err))
		}
		err = errors.Join(errList...)
		app.GetApp().Logger().Error("展开记录失败", "err", err)
		history.record(checkPhase, "获取授权信息失败", err)
		return err
	}
	history.record(checkPhase, "获取授权信息成功", nil)

	cert := currRecord.GetString("certificate")
	expiredAt := currRecord.GetDateTime("expiredAt").Time()

	if cert != "" && time.Until(expiredAt) > time.Hour*24 && currRecord.GetBool("deployed") {
		app.GetApp().Logger().Info("证书在有效期内")
		history.record(checkPhase, "证书在有效期内且已部署，跳过", nil, true)
		return err
	}
	history.record(checkPhase, "检查通过", nil, true)

	// ############2.申请证书
	history.record(applyPhase, "开始申请", nil)

	if cert != "" && time.Until(expiredAt) > time.Hour*24 {
		history.record(applyPhase, "证书在有效期内，跳过", nil)
	} else {
		applicant, err := applicant.Get(currRecord)
		if err != nil {
			history.record(applyPhase, "获取applicant失败", err)
			app.GetApp().Logger().Error("获取applicant失败", "err", err)
			return err
		}
		certificate, err := applicant.Apply()
		if err != nil {
			history.record(applyPhase, "申请证书失败", err)
			app.GetApp().Logger().Error("申请证书失败", "err", err)
			return err
		}
		history.record(applyPhase, "申请证书成功", nil)
		if err = saveCert(ctx, record, certificate); err != nil {
			history.record(applyPhase, "保存证书失败", err)
			app.GetApp().Logger().Error("保存证书失败", "err", err)
			return err
		}
	}

	history.record(applyPhase, "保存证书成功", nil, true)

	// ############3.部署证书
	history.record(deployPhase, "开始部署", nil)
	deployer, err := deployer.Get(currRecord)
	if err != nil {
		history.record(deployPhase, "获取deployer失败", err)
		app.GetApp().Logger().Error("获取deployer失败", "err", err)
		return err
	}

	if err = deployer.Deploy(ctx); err != nil {
		setDeployed(ctx, record, false)
		app.GetApp().Logger().Error("部署失败", "err", err)
		history.record(deployPhase, "部署失败", err)
		return err
	}

	setDeployed(ctx, record, true)
	app.GetApp().Logger().Info("部署成功")
	history.record(deployPhase, "部署成功", nil, true)

	return nil
}

func setDeployed(ctx context.Context, record *models.Record, deployed bool) error {
	record.Set("deployed", deployed)
	if err := app.GetApp().Dao().SaveRecord(record); err != nil {
		return err
	}
	return nil
}

func saveCert(ctx context.Context, record *models.Record, cert *applicant.Certificate) error {
	record.Set("certUrl", cert.CertUrl)
	record.Set("certStableUrl", cert.CertStableUrl)
	record.Set("privateKey", cert.PrivateKey)
	record.Set("certificate", cert.Certificate)
	record.Set("issuerCertificate", cert.IssuerCertificate)
	record.Set("csr", cert.Csr)
	record.Set("expiredAt", time.Now().Add(time.Hour*24*90))

	if err := app.GetApp().Dao().SaveRecord(record); err != nil {
		return err
	}
	return nil
}