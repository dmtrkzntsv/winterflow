package cert

func IsServerCertificateGenerated(m *Manager) bool {
	exists, _ := m.ExistsCACertificate()
	if !exists {
		return false
	}
	exists, _ = m.ExistsCAKey()
	if !exists {
		return false
	}
	exists, _ = m.ExistsServerCertificate()
	if !exists {
		return false
	}
	exists, _ = m.ExistsServerKey()
	if !exists {
		return false
	}
	exists, _ = m.ExistsFullchainCertificate()
	if !exists {
		return false
	}
	return true
}
