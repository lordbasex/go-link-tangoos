package diskops

// ExecError guarda la salida de un comando para el log del agente; a tangod solo
// llega un mensaje estable, nunca la salida cruda (docs/03 §5).
type ExecError struct {
	Bin    string
	Err    error
	Output string
}

func (e *ExecError) Error() string { return e.Bin + ": " + e.Err.Error() }

func (e *ExecError) Unwrap() error { return e.Err }
