@tool
extends EditorExportPlatformExtension

func _get_name() -> String:
	return "Termcade"

func _get_os_name() -> String:
	return "Termcade"

func _get_logo() -> Texture2D:
	var image := Image.create(32, 32, false, Image.FORMAT_RGBA8)
	image.fill(Color("3fc4c9"))
	return ImageTexture.create_from_image(image)

func _get_binary_extensions(_preset: EditorExportPreset) -> PackedStringArray:
	return PackedStringArray(["tgd"])

func _get_platform_features() -> PackedStringArray:
	return PackedStringArray(["termcade", "termcade_framebuffer", "pc"])

func _get_preset_features(_preset: EditorExportPreset) -> PackedStringArray:
	return PackedStringArray()

func _get_export_options() -> Array[Dictionary]:
	return [{"name": "terminal/profile", "type": TYPE_STRING, "default_value": "framebuffer-v1"}]

func _has_valid_export_configuration(_preset: EditorExportPreset, _debug: bool) -> bool:
	set_config_missing_templates(false)
	return true

func _has_valid_project_configuration(_preset: EditorExportPreset) -> bool:
	var main: String = ProjectSettings.get_setting("application/run/main_scene", "")
	if main.is_empty():
		set_config_error("Termcade requires a main scene.")
		return false
	return true

func _can_export(preset: EditorExportPreset, debug: bool) -> bool:
	return _has_valid_export_configuration(preset, debug) and _has_valid_project_configuration(preset)

func _export_project(preset: EditorExportPreset, debug: bool, path: String, _flags: int) -> Error:
	var result: Dictionary = save_pack(preset, debug, path)
	if not result.get("so_files", []).is_empty():
		add_message(EXPORT_MESSAGE_ERROR, "Termcade compatibility", "Native extensions are unsupported.")
		DirAccess.remove_absolute(path)
		return ERR_UNAVAILABLE
	return result.result
