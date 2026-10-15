package main

import (
	"crypto/tls"
	"crypto/x509"
	"os"

	"github.com/lordbasex/go-link-tangoos/internal/clusterpki"
	"github.com/lordbasex/go-link-tangoos/internal/diskops"
	"github.com/lordbasex/go-link-tangoos/internal/gluster"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// Validación de las ops de la segunda etapa del clúster (TLS, bricks en LV, cuotas,
// rebalance, instantáneas).

func validateGlusterTLS(a *agentproto.GlusterTLSArgs) *agentproto.Error {
	n := 0
	for _, s := range []string{a.Cert, a.Key, a.CA} {
		if s != "" {
			n++
		}
	}
	if n != 0 && n != 3 {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "se esperan certificado, clave y CA juntos")
	}
	if n == 3 {
		if len(a.Cert)+len(a.Key)+len(a.CA) > 64<<10 {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "archivos TLS demasiado grandes")
		}
		pair, err := tls.X509KeyPair([]byte(a.Cert), []byte(a.Key))
		if err != nil {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "certificado y clave no forman un par: %v", err)
		}
		// La CA puede traer dos certificados (rotación: la nueva y la anterior).
		cas, err := clusterpki.ParseCerts([]byte(a.CA))
		if err != nil {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "CA inválida")
		}
		pool := x509.NewCertPool()
		for _, ca := range cas {
			if !ca.IsCA {
				return agentproto.Errf(agentproto.CodeInvalidArgs, "CA inválida")
			}
			pool.AddCert(ca)
		}
		leaf, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "certificado inválido")
		}
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}); err != nil {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "el certificado no lo firmó esa CA")
		}
		if err := gluster.ValidCN(leaf.Subject.CommonName); err != nil {
			return badArgs(err)
		}
	}
	if _, err := os.Stat(gluster.TLSCertFile); a.Secure && n == 0 && err != nil {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "TLS de management sin certificado instalado")
	}
	return nil
}

func validateGlusterVolumeTLS(a *agentproto.GlusterVolumeTLSArgs) *agentproto.Error {
	if err := gluster.ValidVolumeName(a.Name); err != nil {
		return badArgs(err)
	}
	if !a.On {
		return nil
	}
	if len(a.CNs) == 0 || len(a.CNs) > 64 {
		return agentproto.Errf(agentproto.CodeInvalidArgs, "se esperan entre 1 y 64 nodos permitidos")
	}
	for _, cn := range a.CNs {
		if err := gluster.ValidCN(cn); err != nil {
			return badArgs(err)
		}
	}
	return nil
}

func validateGlusterBrickLV(a *agentproto.GlusterBrickLVArgs) *agentproto.Error {
	if err := gluster.ValidVolumeName(a.Vol); err != nil {
		return badArgs(err)
	}
	if err := diskops.ValidPoolName(a.Pool); err != nil {
		return badArgs(err)
	}
	switch a.Op {
	case "create":
		if a.Size < 256<<20 || a.Size > 1<<50 {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "tamaño del brick entre 256 MB y 1 PB")
		}
	case "delete":
		if len(a.Self) > 32 {
			return agentproto.Errf(agentproto.CodeInvalidArgs, "demasiados nombres")
		}
		for _, h := range a.Self {
			if err := gluster.ValidHost(h); err != nil {
				return badArgs(err)
			}
		}
	default:
		return agentproto.Errf(agentproto.CodeInvalidArgs, "operación inválida: %q", a.Op)
	}
	return nil
}

func validateGlusterQuota(a *agentproto.GlusterQuotaArgs) *agentproto.Error {
	if err := gluster.ValidVolumeName(a.Name); err != nil {
		return badArgs(err)
	}
	if _, err := gluster.ArgsQuota(a.Name, a.Op, a.Path, a.Limit); err != nil {
		return badArgs(err)
	}
	return nil
}

func validateGlusterRebalance(a *agentproto.GlusterRebalanceArgs) *agentproto.Error {
	if err := gluster.ValidVolumeName(a.Name); err != nil {
		return badArgs(err)
	}
	if _, err := gluster.ArgsRebalance(a.Name, a.Action); err != nil {
		return badArgs(err)
	}
	return nil
}

func validateGlusterSnapshot(a *agentproto.GlusterSnapshotArgs) *agentproto.Error {
	if err := gluster.ValidVolumeName(a.Name); err != nil {
		return badArgs(err)
	}
	if _, err := gluster.ArgsSnapshot(a.Op, a.Snap, a.Name, a.Desc); err != nil {
		return badArgs(err)
	}
	return nil
}
