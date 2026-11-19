"""Filter the pinned sing-box 1.14.3 JSON schema to Pax's editable single outbound.

Input: sing-box `schema` output (or schema.Generate(option.Outbound)). No upstream
Go types enter the MCP/controller packages. Review this snapshot when upgrading.
"""
import argparse
import json
from pathlib import Path

SUPPORTED = {'anytls', 'hysteria2', 'vless', 'vmess', 'trojan', 'shadowsocks', 'socks', 'http'}
FORBIDDEN = {'detour', 'bind_interface', 'inet4_bind_address', 'inet6_bind_address',
             'routing_mark', 'reuse_addr', 'domain_resolver', 'network_strategy',
             'network_type', 'fallback_network_type', 'certificate', 'key',
             'client_certificate', 'client_key', 'plugin', 'plugin_opts',
             'netns', 'realm', 'client_metadata', 'username', 'password', 'uuid',
             'short_id', 'path'}


def generate(source, destination):
    document = json.loads(Path(source).read_text())
    definitions = document['$defs']
    definitions['Outbound']['oneOf'] = [item for item in definitions['Outbound']['oneOf']
        if item.get('properties', {}).get('type', {}).get('const') in SUPPORTED]
    transports = {item['properties']['type']['const']: item
                  for item in definitions['V2RayTransport']['oneOf']}
    definitions['V2RayTransport']['oneOf'] = [transports[key] for key in ('ws', 'httpupgrade')]
    grpc_transport = transports['grpc']
    grpc_transport['properties'] = {key: grpc_transport['properties'][key]
                                    for key in ('type', 'service_name')}
    definitions['VLESSGRPCTransport'] = grpc_transport
    header_value = {'anyOf': [{'type': 'string', 'maxLength': 65536},
        {'type': 'array', 'items': {'type': 'string', 'maxLength': 65536}, 'maxItems': 1000}]}
    definitions['HTTPHeader'] = {'type': 'object', 'additionalProperties': False,
                                  'properties': {'Host': header_value}}

    def filter_node(node):
        if isinstance(node, list):
            return [filter_node(value) for value in node]
        if not isinstance(node, dict):
            return node
        node = {key: filter_node(value) for key, value in node.items()
                if not key.startswith('x-') and key not in {'description', 'examples'}}
        if 'properties' in node:
            node['properties'] = {key: value for key, value in node['properties'].items()
                if key not in FORBIDDEN and not key.endswith('_path')}
            if 'required' in node:
                node['required'] = [key for key in node['required'] if key in node['properties']]
        if node.get('type') == 'string':
            node.setdefault('maxLength', 65536)
        if node.get('type') == 'array':
            node.setdefault('maxItems', 1000)
        return node

    definitions = filter_node(definitions)
    for variant in definitions['Outbound']['oneOf']:
        props = variant['properties']
        props['server']['maxLength'] = 253
        props['server_port']['minimum'] = 1
        variant['required'] = ['type', 'server', 'server_port']
        protocol = props['type']['const']
        if protocol == 'socks':
            props['version'] = {'type': 'string', 'enum': ['5']}
        if protocol == 'vless':
            grpc = {'$ref': '#/$defs/VLESSGRPCTransport'}
            props['transport'] = {'oneOf': [props['transport'], grpc]}
            variant['if'] = {'required': ['transport'], 'properties': {'transport': grpc}}
            variant['then'] = {
                'required': ['tls'],
                'properties': {
                    'flow': {'const': ''},
                    'tls': {
                        'required': ['enabled'],
                        'properties': {
                            'enabled': {'const': True},
                            'alpn': {'anyOf': [
                                {'const': 'h2'},
                                {'type': 'array', 'maxItems': 0},
                                {'type': 'array', 'contains': {'const': 'h2'}},
                            ]},
                        },
                    },
                },
            }
    # Keep only referenced definitions. Reject external refs at generation time.
    needed = set()

    def collect(node):
        if isinstance(node, dict):
            if '$ref' in node:
                assert node['$ref'].startswith('#/$defs/')
                key = node['$ref'].split('/')[-1]
                if key not in needed:
                    needed.add(key)
                    collect(definitions[key])
            for value in node.values():
                collect(value)
        elif isinstance(node, list):
            for value in node:
                collect(value)
    collect({'$ref': '#/$defs/Outbound'})
    result = {key: definitions[key] for key in sorted(needed)}
    Path(destination).write_text(json.dumps(result, ensure_ascii=False, separators=(',', ':'))+'\n')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('source')
    parser.add_argument('destination')
    arguments = parser.parse_args()
    generate(arguments.source, arguments.destination)
