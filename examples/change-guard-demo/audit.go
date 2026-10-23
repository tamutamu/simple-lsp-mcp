package changeguarddemo

var auditEvents int

func Audit(orderID string) {
	auditEvents++
}
