package ports

import "time"

// Clock abstrai o relógio para permitir avançar o watermark de forma
// determinística nos testes, sem time.Sleep.
type Clock interface {
	Now() time.Time
}

// RealClock é a implementação de produção do Clock (UTC).
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now().UTC() }
