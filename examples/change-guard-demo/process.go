package changeguarddemo

func CompleteOrder(orderID string) string {
	Audit(orderID)
	return "completed:" + orderID
}
