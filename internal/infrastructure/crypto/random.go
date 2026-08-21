package crypto

import authdomain "agentcanvas/internal/domain/auth"

func RandomURLToken(nBytes int) (string, error) {
	return authdomain.RandomURLToken(nBytes)
}
