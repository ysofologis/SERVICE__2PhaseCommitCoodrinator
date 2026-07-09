package pax

type PrepareResponse struct {
	Vote   string `json:"vote"`   // "yes" or "no"
	Reason string `json:"reason"` // optional
}

type TxStatusResponse struct {
	Status string `json:"status"`
}
