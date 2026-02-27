# portunus_interfaces — Pure virtual interface headers (no source files)
#
# These define the contracts that the FSM programs against.
# Concrete implementations live in drivers/ and implement these interfaces.
idf_component_register(
    INCLUDE_DIRS
        "include"
    REQUIRES
        portunus_types
)
