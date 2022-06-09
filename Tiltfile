docker_build(
    'kac-ca-injector',
    context = '.'
)

k8s_yaml(['./config/namespace.yaml', './config/secret.yaml'])
k8s_yaml(helm('../../helm_charts/admission-controller', name='ca-injector', namespace='botland', values='./values/admission-controller.yaml'))