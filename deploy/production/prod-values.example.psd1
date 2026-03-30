@{
  # Registry and image ownership
  RegistryOrg = "your-org"
  Repository = "feature-flags-control-plane"

  # Public DNS base domain for ingress hosts:
  # api.<RootDomain>, demo.<RootDomain>, grafana.<RootDomain>
  RootDomain = "flagplane.example.com"

  # cert-manager ACME registration email
  AcmeEmail = "platform-ops@example.com"

  # External secret manager details
  KeyVaultName = "your-keyvault-name"

  # Alert routing
  PagerDutyRoutingKey = "your-pagerduty-routing-key"

  # Promotion workflow namespaces
  StagingNamespace = "flagplane-staging"
  ProductionNamespace = "flagplane"
}
