# DNS and TLS Automation Guide

This guide finalizes production DNS and certificate automation using ExternalDNS and cert-manager.

## Inputs Required

- Public DNS zone for production domains
- Ingress controller class (default: `nginx`)
- cert-manager installed
- ExternalDNS installed and authorized for DNS provider

## Hostnames

Set these hostnames in `deploy/production/patch-production-overrides.yaml` and `deploy/production/cert-manager-certificate.yaml`:

- `api.flagplane.example.com`
- `demo.flagplane.example.com`
- `grafana.flagplane.example.com`

## Certificate Automation

Apply issuer/certificate manifest:

```bash
kubectl apply -f deploy/production/cert-manager-certificate.yaml
```

Check readiness:

```bash
kubectl get certificate flagplane-tls -n flagplane
kubectl describe certificate flagplane-tls -n flagplane
```

## DNS Automation

The production ingress patch adds `external-dns.alpha.kubernetes.io/hostname` annotations.

After `kubectl apply -k deploy/production`, verify record creation:

```bash
kubectl get ingress -n flagplane
nslookup api.flagplane.example.com
nslookup demo.flagplane.example.com
nslookup grafana.flagplane.example.com
```

## Renewal and Monitoring

- cert-manager automatically renews certificates before expiry.
- Alert if certificate expiry is within 14 days.
- Review cert-manager events during each renewal cycle.

## Failure Recovery

If issuance fails:

1. Check ACME solver events in cert-manager namespace.
2. Verify ingress class and HTTP-01 reachability.
3. Validate ExternalDNS record propagation.
4. Reconcile by reapplying production overlay.
