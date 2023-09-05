package ioc

import (
	"webook/internal/service/sms"
	"webook/internal/service/sms/memory"
)

func InitSMSService() sms.Service {
	// 换内存，还是换别的
	return memory.NewService()
}
