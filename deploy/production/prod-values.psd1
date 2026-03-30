@{
  # Registry and image ownership
  RegistryOrg = "jems0906"
  Repository = "feature-flags-control-plane"

  # Public DNS base domain for ingress hosts:
  # api.<RootDomain>, demo.<RootDomain>, grafana.<RootDomain>
  RootDomain = "flagplane.jems0906.dev"

  # cert-manager ACME registration email
  AcmeEmail = "ops@jems0906.dev"

  # External secret manager details
  KeyVaultName = "flagplane-kv"

  # Alert routing (replace with a real key before live traffic is routed)
  PagerDutyRoutingKey = "00000000000000000000000000000000"

  # Promotion workflow namespaces
  StagingNamespace = "flagplane-staging"
  ProductionNamespace = "flagplane"
}
