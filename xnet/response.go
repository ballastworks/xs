package xnet

type Response[T any] struct {
	val *T
}

func (tr *Response[T]) SetProtocolResponse(resp *T) {
	tr.val = resp
}

func (tr Response[T]) ProtocolResponse() *T {
	return tr.val
}
