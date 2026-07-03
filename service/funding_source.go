package service

// ---------------------------------------------------------------------------
// FundingSource — 计费来源接口（兼容历史钱包字段）
// ---------------------------------------------------------------------------

// FundingSource 抽象了预扣费来源。
type FundingSource interface {
	// Source 返回资金来源标识。
	Source() string
	// PreConsume 从该来源预扣 amount 额度
	PreConsume(amount int) error
	// Settle 根据差额调整来源（正数补扣，负数退还）
	Settle(delta int) error
	// Refund 退还所有预扣费
	Refund() error
}

// ---------------------------------------------------------------------------
// WalletFunding — 常规 API Key 计费来源
// ---------------------------------------------------------------------------

type WalletFunding struct {
	userId   int
	consumed int // 兼容历史字段；用户余额已不再扣减
}

func (w *WalletFunding) Source() string { return BillingSourceWallet }

func (w *WalletFunding) PreConsume(amount int) error {
	// 钱包余额模块已移除。常规请求只消耗 API Key 自身额度，
	// 这里保留 wallet 来源名称用于兼容历史日志和任务字段。
	_ = amount
	return nil
}

func (w *WalletFunding) Settle(delta int) error {
	_ = delta
	return nil
}

func (w *WalletFunding) Refund() error {
	return nil
}
