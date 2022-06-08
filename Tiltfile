docker_build(
    'kac-ca-injector',
    context = '.'
)

k8s_yaml(['./config/namespace.yaml', './config/secret.yaml'])
k8s_yaml(helm('../../helm_charts/_deployment', name='ca-injector', namespace='botland', values='./values/deployment.yaml'))
k8s_yaml('./config/mutate.yaml')