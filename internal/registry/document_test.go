package registry

import "testing"

func TestDocumentCheckDigits(t *testing.T) {
	for _, d := range []string{"52998224725", "11144477735"} {
		if !validCPF(d) {
			t.Errorf("CPF %s should be valid", d)
		}
	}
	for _, d := range []string{"52998224724", "11111111111", "123"} {
		if validCPF(d) {
			t.Errorf("CPF %s should be invalid", d)
		}
	}
	for _, d := range []string{"11222333000181", "45997418000153"} {
		if !validCNPJ(d) {
			t.Errorf("CNPJ %s should be valid", d)
		}
	}
	for _, d := range []string{"11222333000180", "00000000000000"} {
		if validCNPJ(d) {
			t.Errorf("CNPJ %s should be invalid", d)
		}
	}
}
