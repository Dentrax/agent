{
  local k = import 'ksonnet-util/kausal.libsonnet',
  local configMap = k.core.v1.configMap,

  tempo_config:: {
    search_enabled: $._config.search_enabled,
    server: {
      http_listen_port: $._config.tempo.port,
    },
    distributor: {
      receivers: $._config.receivers,
    },
    ingester: {
    },
    compactor: {
      compaction: {
        block_retention: '24h',
      },
    },
    memberlist: {
      abort_if_cluster_join_fails: false,
      bind_port: 7946,
      join_members: [
        'localhost:7946'
      ],
    },
    storage: {
      trace: {
        backend: 'local',
        wal: {
          path: '/var/tempo/wal',
        },
        'local': {
          path: '/tmp/tempo/traces',
        },
      },
    },
    querier: {
      frontend_worker: {
        frontend_address: 'localhost:9095',
      },
    },
  },
}