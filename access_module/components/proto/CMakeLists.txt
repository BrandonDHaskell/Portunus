# components/proto — Nanopb-generated message types from portunus.proto
#
# Contains pre-generated .pb.c / .pb.h files produced by nanopb_generator
# from the shared contract at proto/portunus/v1/portunus.proto.
#
# The generator preserves the proto package path, so the files live under
# portunus/v1/ within this component.  INCLUDE_DIRS exposes the component
# root so that other code includes the header as:
#
#   #include "portunus/v1/portunus.pb.h"
#
# To regenerate after a .proto change (run from the Portunus project root):
#
#   nanopb_generator \
#       -I proto \
#       -D access_module/components/proto \
#       -f proto/nanopb/portunus.options \
#       proto/portunus/v1/portunus.proto
#
# The nanopb runtime library is pulled in as an ESP-IDF managed component
# (see idf_component.yml in this directory).

idf_component_register(
    SRCS
        "portunus/v1/portunus.pb.c"
    INCLUDE_DIRS
        "."
    REQUIRES
        nanopb
)