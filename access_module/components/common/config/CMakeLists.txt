# common/config — Build-time configuration (Kconfig-driven)
#
# Header-only component providing pin assignments, timing constants,
# network parameters, and security settings. Values should be pulled
# from Kconfig (sdkconfig) where possible so that dev/prod differences
# are managed through sdkconfig overlays rather than scattered #defines.

idf_component_register(
    INCLUDE_DIRS
        "include"
    REQUIRES
        types
)