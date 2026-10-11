extends SceneTree

func _initialize() -> void:
	var args := OS.get_cmdline_user_args()
	if args.size() != 1:
		quit(2)
		return
	var project: String = args[0]
	var config := ConfigFile.new()
	if config.load(project.path_join("project.godot")) != OK:
		quit(2)
		return
	var plugins: PackedStringArray = config.get_value("editor_plugins", "enabled", PackedStringArray())
	if not plugins.has("res://addons/termcade/plugin.cfg"):
		plugins.append("res://addons/termcade/plugin.cfg")
	config.set_value("editor_plugins", "enabled", plugins)
	if config.save(project.path_join("project.godot")) != OK:
		quit(2)
		return
	var presets := ConfigFile.new()
	presets.load(project.path_join("export_presets.cfg"))
	var index := 0
	while presets.has_section("preset.%d" % index):
		if presets.get_value("preset.%d" % index, "name", "") == "Termcade":
			print("TERMCADE SETUP OK")
			quit()
			return
		index += 1
	var section := "preset.%d" % index
	presets.set_value(section, "name", "Termcade")
	presets.set_value(section, "platform", "Termcade")
	presets.set_value(section, "runnable", false)
	presets.set_value(section, "export_filter", "all_resources")
	presets.set_value(section, "include_filter", "addons/termcade/*.gd")
	presets.set_value(section, "exclude_filter", "")
	presets.set_value(section, "script_export_mode", 1)
	presets.set_value(section, "export_path", "build/game.tgd")
	presets.set_value(section + ".options", "terminal/profile", "framebuffer-v1")
	if presets.save(project.path_join("export_presets.cfg")) != OK:
		quit(2)
		return
	print("TERMCADE SETUP OK")
	quit()
