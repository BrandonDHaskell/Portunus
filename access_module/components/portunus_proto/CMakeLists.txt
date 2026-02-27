# portunus_proto — Nanopb-generated message types from portunus.proto
idf_component_register(
    SRCS
        "portunus/v1/portunus.pb.c"
    INCLUDE_DIRS
        "."
    REQUIRES
        nanopb
)
