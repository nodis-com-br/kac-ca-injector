docker_build(
    'kac-ca-injector',
    context = '.'
)

k8s_yaml([
    './config/namespace.yaml',
    './config/secret.yaml',
    './config/clusterrole.yaml',
    './config/clusterrolebinding.yaml',
    './config/webhook.yaml',
    './config/service.yaml',
    './config/serviceaccount.yaml',
    './config/deployment.yaml',
])
