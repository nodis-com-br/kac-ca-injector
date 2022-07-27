docker_build(
    'kac-ca-injector',
    context = '.'
)

k8s_yaml([
    './demo/namespace.yaml',
    './demo/secret.yaml',
    './demo/clusterrole.yaml',
    './demo/clusterrolebinding.yaml',
    './demo/webhook.yaml',
    './demo/service.yaml',
    './demo/serviceaccount.yaml',
    './demo/deployment.yaml',
])
