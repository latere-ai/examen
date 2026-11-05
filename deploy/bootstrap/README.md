# Bootstrap resources

The release pipeline never reads this directory. Everything it applies runs
again on every release, so everything it applies must be re-appliable, and
the resources here are not: a token Secret is minted once, an identity that
could rewrite its own Role could grant itself anything, and an object that
selects another service's Pods is not one this release may write.

`deploy/prod` is Latere's production tree for the core, applied by the
`deploy` job of `.github/workflows/release.yml`. The init container of its
Deployment runs `examend migrate` before each replica serves, under the
migrator's lock, so there is no migration Job to run before a release. The
Secrets the Deployment reads (`examen-db`, `examen-authorizer`,
`examen-keys`, `examen-minter` and `platformd-internal-ca`) are created
outside this tree and keep their names; `examen-db` and the pool it names
come from the terraform repository, and a Pod whose Secret is missing does
not start.

| File | Applied by | When |
|---|---|---|
| `rollout-identity.yaml` | an operator | once, and again when a kind is added to `deploy/prod` |

## The rollout identity

`rollout-identity.yaml` is the account the release pipeline deploys with: a
ServiceAccount, a Role and RoleBinding in the `latere` namespace over the
kinds `deploy/prod` contains, and the token Secret bound to the account. An
installation elsewhere changes the namespace before applying it. A kind
added to `deploy/prod` is added to the Role and applied again by an
operator.

```sh
kubectl --context do-fra1-latere-k8s apply -f deploy/bootstrap/rollout-identity.yaml

NS=latere SA=examend-rollout
kubeconfig=$(mktemp) && chmod 600 "$kubeconfig"
KUBECONFIG="$kubeconfig" kubectl config set-cluster latere-k8s \
  --server="$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')"
KUBECONFIG="$kubeconfig" kubectl config set clusters.latere-k8s.certificate-authority-data \
  "$(kubectl --context do-fra1-latere-k8s -n "$NS" get secret "$SA-token" -o jsonpath='{.data.ca\.crt}')"
KUBECONFIG="$kubeconfig" kubectl config set-credentials "$SA" \
  --token="$(kubectl --context do-fra1-latere-k8s -n "$NS" get secret "$SA-token" -o jsonpath='{.data.token}' | base64 -d)"
KUBECONFIG="$kubeconfig" kubectl config set-context "$SA" \
  --cluster=latere-k8s --user="$SA" --namespace="$NS"
KUBECONFIG="$kubeconfig" kubectl config use-context "$SA"

# The server-side dry run authorizes every object a release applies without
# changing any, so a Role that misses a kind fails here and not in a release.
KUBECONFIG="$kubeconfig" kubectl apply -k deploy/prod --dry-run=server

gh secret set EXAMEN_KUBECONFIG --repo latere-ai/examen < "$kubeconfig"
rm "$kubeconfig"
gh variable set EXAMEN_RELEASE_DEPLOY --repo latere-ai/examen --body 1
```

The `deploy` job of `.github/workflows/release.yml` runs only while the
repository variable `EXAMEN_RELEASE_DEPLOY` is set, so a fork, which sets
neither it nor the secret, publishes every artifact and deploys nothing.
Unsetting the variable turns the deploy off again.

Rotating the credential is deleting the token Secret, applying the identity
file again, and running the same steps.

## Admitting the core to platformd

The policy that admits the core's Pods to platformd's internal TLS listener,
`examend-platformd-tls`, selects platformd's Pods, so platformd's release
applies it from `deploy/prod` in `latere-ai/platform`. No operator step
remains for it.
