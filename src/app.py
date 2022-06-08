import base64
import copy
import http
import jsonpatch
import os
import requests
from kubernetes import client, config
from kubernetes.client.exceptions import ApiException
from flask import Flask, jsonify, request

app = Flask(__name__)

configmap_name = os.environ["CA_BUNDLE_CONFIGMAP"]
ca_bundle_filename = os.environ["CA_BUNDLE_FILENAME"]
ca_bundle_url = os.environ["CA_BUNDLE_URL"]
ca_bundle_annotation = os.environ["CA_BUNDLE_ANNOTATION"]


def create_configmap(v1_api, namespace):
    r = requests.get(ca_bundle_url, stream=True)

    if r.status_code != 200:
        raise Exception(f"fetch {ca_bundle_url} failed: {r.status_code}")
    else:
        ca_bundle = r.content.decode('ascii')

    v1_api.create_namespaced_config_map(namespace=namespace, body=client.V1ConfigMap(
        api_version="v1",
        kind="ConfigMap",
        metadata=client.V1ObjectMeta(
            name=configmap_name,
            namespace=namespace
        ),
        data={
            ca_bundle_filename: ca_bundle
        }
    ))


@app.route('/mutate', methods=['POST'])
def mutate():
    spec = request.json['request']['object']
    modified_spec = copy.deepcopy(spec)

    if modified_spec['metadata']['annotations'].get(ca_bundle_annotation) == 'true':
        namespace = modified_spec['metadata']['namespace']
        config.load_incluster_config()
        v1_api = client.CoreV1Api()

        try:
            v1_api.read_namespaced_config_map(namespace=namespace, name=configmap_name)
        except ApiException:
            create_configmap(v1_api, namespace)

        volume = {
            'name': 'ca-bundle',
            'configMap': {
                'name': configmap_name,
                'defaultMode': 420
            }
        }
        volume_mount = {
            'name': 'ca-bundle',
            'mountPath': f'/etc/ssl/certs/{ca_bundle_filename}',
            'subPath': ca_bundle_filename
        }

        if 'volumes' in modified_spec['spec']:
            modified_spec['spec']['volumes'].append(volume)
        else:
            modified_spec['spec']['volumes'] = [volume]

        if 'initContainers' in modified_spec['spec']:
            for c in modified_spec['spec']['initContainers']:
                if 'volumeMounts' in c:
                    c['volumeMounts'].append(volume_mount)
                else:
                    c['volumeMounts'] = [volume_mount]

        for c in modified_spec['spec']['containers']:
            if 'volumeMounts' in c:
                c['volumeMounts'].append(volume_mount)
            else:
                c['volumeMounts'] = [volume_mount]

    patch = jsonpatch.JsonPatch.from_diff(spec, modified_spec)
    return jsonify({
        'apiVersion': 'admission.k8s.io/v1',
        'kind': 'AdmissionReview',
        'response': {
            'allowed': True,
            'uid': request.json['request']['uid'],
            'patch': base64.b64encode(str(patch).encode()).decode(),
            'patchType': 'JSONPatch',
        }
    })


@app.route('/health', methods=['GET'])
def health():
    return 'Ok', http.HTTPStatus.OK


if __name__ == '__main__':
    app.run(host='0.0.0.0', debug=True)  # pragma: no cover
